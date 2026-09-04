# Recipes

These are workflows you can build out of features in hoard.

- [A wantlist](#a-wantlist)
- [Moving cards in bulk](#moving-cards-in-bulk)

## A wantlist

There is no wantlist screen. Use a binder that does not count toward your
collection instead. Press <kbd>x</kbd> on a binder in the TUI to exclude it, or
do it from the CLI:

```console
$ hoard binder new want
Created binder #2 "want"
$ hoard binder exclude want
Binder "want" is no longer counted toward your collection
```

Cards you are hunting go in it the same way anything else goes into a binder.

```console
$ hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre --binder want
✓ Added 1× Ulamog, the Infinite Gyre (uma/7) as nonfoil into want · $36.32
$ pbpaste | hoard add --file - --binder want
```

Excluding a binder only changes the accounting. Cards in `want` are still
priced by `hoard update-prices`, and they still turn up in `hoard movers`. You
can watch them too, so hoard tells you when one hits your price:

```console
$ hoard watch add "Ulamog, the Infinite Gyre" --under 30
Watching Ulamog, the Infinite Gyre (uma/7) nonfoil: under $30.00.
```

`hoard report` leaves the binder out of the total.
`hoard binder list` still totals it up, so you can see what finishing the
list would cost:

```console
$ hoard binder list
ID  NAME    CARDS   VALUE
 1  Binder      0   $0.00
 2  Want *      1  $36.32
* not counted toward your collection
```

### Getting a card off the list

When you get a card on the list, move it out of `want` and into the binder you
keep it in.

That happens in the browser. Select the card and press <kbd>enter</kbd> for its
detail. <kbd>up</kbd> drops you into the row for the copy you hold, and
<kbd>right</kbd> walks along that row to its last field, the binder.
<kbd>enter</kbd> there asks which binder to move it to.

The card counts toward your collection as soon as it lands in a counted binder.
<kbd>esc</kbd> backs out to the browser, and <kbd>u</kbd> undoes the move.

## Moving cards in bulk

For one card, the above is the quickest way. For a lot of them at once,
`hoard move` takes a holdings document on stdin and files every card in it into
one binder:

```console
$ hoard export --binder want --json | hoard move --to Binder
```

`hoard export` chooses the holdings and `hoard move` acts on them, so any
[filter](filtering.md) is also a bulk selection. [Scripting](scripting.md) goes
further, narrowing the document with `jq` between the two.
