package insights

import (
	"fmt"
	"time"
)

// BoardCard is one insight as a board will draw it, plus the one thing the render
// layer would otherwise have to work out for itself: whether this card already pins
// a window of its own.
type BoardCard struct {
	Insight
	// OwnWindow is true when the saved params already name a window (days/hours/
	// from/to). The board's range must NOT be applied to such a card: the user
	// deliberately saved "signups, last 24h", and silently stretching that to the
	// board's 30 days would print a different number under the same title.
	OwnWindow bool `json:"own_window"`
}

// BoardView is one dashboard render in a single read: the board, the window its
// cards cover, and the cards themselves. Range is stated rather than implied,
// because a dashboard that shows numbers without saying which window produced them
// is unciteable — the same rule every report payload here follows.
type BoardView struct {
	Board Board         `json:"board"`
	Range ResolvedRange `json:"range"`
	Cards []BoardCard   `json:"cards"`
}

// windowKeys are the params that pin a report to a window of its own. They are the
// keys the dashboard and the /v1 report endpoints already read, listed here so that
// "does this card set its own window" is answered in exactly one place instead of
// being re-guessed by every caller that inherits a board range.
var windowKeys = [...]string{"days", "hours", "from", "to"}

// Render assembles everything one board render needs. now is a parameter and never
// time.Now(): the resolved window is echoed to the client and pinned by tests, and a
// function that reads the clock itself can deliver neither a reproducible test nor a
// window the caller can verify.
func (s *Store) Render(boardID string, now time.Time) (BoardView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i := range s.boards {
		if s.boards[i].ID == boardID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return BoardView{}, fmt.Errorf("%w: %q", ErrNoSuchBoard, boardID)
	}
	rr, err := s.boards[idx].Range.Resolve(now)
	if err != nil {
		// Refuse rather than fall back to the default 30 days: a board that says
		// "1–14 March" rendering the last 30 days would put the wrong numbers under
		// the right title, which is indistinguishable from a correct answer.
		return BoardView{}, fmt.Errorf("board %q has an unusable range: %w", boardID, err)
	}
	view := BoardView{Board: copyBoard(s.boards[idx]), Range: rr, Cards: []BoardCard{}}
	for _, it := range s.items {
		if it.BoardID != boardID {
			continue
		}
		view.Cards = append(view.Cards, BoardCard{Insight: copyInsight(it), OwnWindow: hasOwnWindow(it.Params)})
	}
	return view, nil
}

// hasOwnWindow reports whether saved params already name a time window. An empty
// value does not count: `days=` is a param that was serialised, not a window a user
// chose, and treating it as one would strand the card outside the board's range.
func hasOwnWindow(params map[string]string) bool {
	for _, k := range windowKeys {
		if params[k] != "" {
			return true
		}
	}
	return false
}

// MergeFilters folds the board's default filters underneath a card's own and returns
// the set the query should actually run with. A key the card sets WINS: a board
// default is a convenience, and one that silently overwrote a filter the user typed
// into the report itself would answer a different question than the card's title
// claims.
//
// It takes the card's filters as a plain map so this package never has to know the
// query layer's chip grammar — the caller parses, this decides precedence, and the
// rule therefore exists in one place rather than once per calling surface.
func (b Board) MergeFilters(own map[string]string) map[string]string {
	out := make(map[string]string, len(b.Filters)+len(own))
	for k, v := range b.Filters {
		out[k] = v
	}
	for k, v := range own {
		out[k] = v
	}
	return out
}
