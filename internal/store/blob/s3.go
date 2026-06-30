package blob

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// S3 is an S3-compatible object-storage backend implemented with stdlib only — no AWS
// SDK, so the binary stays single and static. It speaks plain HTTP with SigV4 signing,
// which works against AWS S3, Cloudflare R2, Tigris, Backblaze B2, and MinIO. This is the
// cold tier for the cloud: per-tenant prefixes, infinite history at pennies/GB.
type S3 struct {
	endpoint string // https://s3.us-east-1.amazonaws.com or https://<acct>.r2.cloudflarestorage.com
	region   string
	bucket   string
	ak, sk   string
	prefix   string // optional key prefix (per-tenant isolation)
	hc       *http.Client
}

// NewS3 builds an S3 backend. region may be "auto" for R2. prefix (optional) namespaces
// every key — pass a per-tenant value in the cloud so one bucket holds many tenants.
func NewS3(endpoint, region, bucket, accessKey, secretKey, prefix string) (*S3, error) {
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("blob: s3 needs endpoint, bucket, access key, and secret key")
	}
	if region == "" {
		region = "auto"
	}
	return &S3{
		endpoint: strings.TrimRight(endpoint, "/"),
		region:   region,
		bucket:   bucket,
		ak:       accessKey,
		sk:       secretKey,
		prefix:   strings.Trim(prefix, "/"),
		hc:       &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (s *S3) fullKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

func (s *S3) objURL(key string) string {
	return s.endpoint + "/" + s.bucket + "/" + encodePath(s.fullKey(key))
}

func (s *S3) Put(key string, data []byte) error {
	req, err := http.NewRequest(http.MethodPut, s.objURL(key), bytes.NewReader(data))
	if err != nil {
		return err
	}
	s.sign(req, data)
	return do2xx(s.hc, req, "put "+key)
}

func (s *S3) Get(key string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, s.objURL(key), nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, nil)
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, os.ErrNotExist // matches Local: absent object is os.ErrNotExist
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("blob: s3 get %s -> %d: %s", key, resp.StatusCode, b)
	}
	return io.ReadAll(resp.Body)
}

func (s *S3) Delete(key string) error {
	req, err := http.NewRequest(http.MethodDelete, s.objURL(key), nil)
	if err != nil {
		return err
	}
	s.sign(req, nil)
	resp, err := s.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode/100 == 2 {
		return nil
	}
	return fmt.Errorf("blob: s3 delete %s -> %d", key, resp.StatusCode)
}

// List returns keys under prefix (with the per-tenant prefix stripped back off), paging
// through ListObjectsV2 until the bucket is exhausted.
func (s *S3) List(prefix string) ([]string, error) {
	full := s.fullKey(prefix)
	var keys []string
	token := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("prefix", full)
		if token != "" {
			q.Set("continuation-token", token)
		}
		u := s.endpoint + "/" + s.bucket + "?" + canonicalQuery(q)
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		s.sign(req, nil)
		resp, err := s.hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			return nil, rerr // don't parse a truncated response as a complete listing
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("blob: s3 list -> %d: %s", resp.StatusCode, body[:min(len(body), 512)])
		}
		var res struct {
			Contents []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}
		if err := xml.Unmarshal(body, &res); err != nil {
			return nil, err
		}
		for _, c := range res.Contents {
			k := c.Key
			if s.prefix != "" {
				if !strings.HasPrefix(k, s.prefix+"/") {
					continue // defensive: never hand back a key outside this tenant's prefix
				}
				k = strings.TrimPrefix(k, s.prefix+"/")
			}
			keys = append(keys, k)
		}
		if !res.IsTruncated || res.NextContinuationToken == "" {
			break
		}
		token = res.NextContinuationToken
	}
	sort.Strings(keys)
	return keys, nil
}

func do2xx(hc *http.Client, req *http.Request, what string) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("blob: s3 %s -> %d: %s", what, resp.StatusCode, b)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// --- AWS Signature Version 4 (stdlib) ---

func (s *S3) sign(req *http.Request, body []byte) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := hex.EncodeToString(sha256Sum(body))

	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + req.URL.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery, // already canonical (sorted, AWS-encoded) for List; empty otherwise
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := dateStamp + "/" + s.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(sha256Sum([]byte(canonicalRequest))),
	}, "\n")

	signature := hex.EncodeToString(hmacSHA256(signingKey(s.sk, dateStamp, s.region, "s3"), []byte(stringToSign)))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.ak+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func signingKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

// canonicalQuery encodes query params the AWS way: sorted, RFC3986, space as %20.
func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, awsEncode(k, false)+"="+awsEncode(v, false))
		}
	}
	return strings.Join(parts, "&")
}

// encodePath AWS-encodes an object key, preserving '/' between segments.
func encodePath(key string) string {
	segs := strings.Split(key, "/")
	for i, s := range segs {
		segs[i] = awsEncode(s, false)
	}
	return strings.Join(segs, "/")
}

// awsEncode percent-encodes per RFC3986: unreserved chars pass through, everything else
// is %XX (uppercase). encodeSlash controls whether '/' is encoded.
func awsEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
