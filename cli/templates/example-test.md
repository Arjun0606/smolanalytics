# Checkout

<!--
One heading is one test. The heading names it, and that name is also the recording's filename, so
renaming a heading throws the recording away and the agent runs the test fresh. The sentence under
the heading is the whole test.

Write what a careful person would check, and say what you expect to SEE. "Checkout works" cannot
fail usefully. Name the page, the control, and the evidence.

Never put a real password or card number in here — this file is committed. Point the tests at a
seeded account on staging and a provider test card.
-->

## A shopper can add an item to the cart

From the storefront, open the first product, add it to the cart, and check that the cart shows one
line for that product at the price the product page listed.

## The cart survives a reload

With one item in the cart, reload the page and check the cart still shows that item and the same
total.

## Checkout asks for payment before it confirms anything

Go to the cart with one item, click through to checkout, and check that an order confirmation is
never shown before card details have been entered.

## An expired card is refused with a message that says so

At checkout, pay with card number 4000 0000 0000 0069, any future-looking name, CVC 123, and check
that the page stays on checkout and says the card is expired — not a generic "something went wrong".

## An empty cart says it is empty

Open the cart with nothing in it and check it says the cart is empty and offers a way back to the
storefront, rather than showing a total of 0 with a checkout button.
