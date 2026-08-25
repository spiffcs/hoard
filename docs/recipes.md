# Recipes

Workflows that fall out of features hoard already has, rather than screens of
their own.

- [A wantlist](#a-wantlist)
- [Moving cards in bulk](#moving-cards-in-bulk)

## A wantlist

There is no wantlist screen, but a user can set a binder that does not count
toward your collection (using the <kbd>x</kbd> key in the TUI or through the CLI
below) as a wantlist.

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

Excluding a binder changes the accounting. Everything in our example `want` is
still priced by `hoard update-prices` and it will still turn up in
`hoard movers`. You can add watches to cards in excluded binders as well to
trigger on when one hits a certain price.

```console
$ hoard watch add "Ulamog, the Infinite Gyre" --under 30
Watching Ulamog, the Infinite Gyre (uma/7) nonfoil: under $30.00.
```

What exclusion gives you is that none of it counts as yours. `hoard report`
leaves the binder out of the total, so a wantlist can never inflate what your
collection is worth, while `hoard binder list` still totals it up so you can see
what finishing the list would cost:

```console
$ hoard binder list
ID  NAME    CARDS   VALUE
 1  Binder      0   $0.00
 2  Want *      1  $36.32
* not counted toward your collection
```

### Getting a card off the list

When you actually get a card on the list it's as easy as moving it out of `want`
and into which ever binder you have stored to card in.

That happens in the browser: select the card, <kbd>enter</kbd> for its detail,
<kbd>up</kbd> to drop into the row for the copy you hold, then <kbd>right</kbd>
along that row to its last field which is the binder it is in. <kbd>enter</kbd>
there asks which binder to move it to. It counts toward your collection from the
moment it lands in a counted binder; <kbd>esc</kbd> back out to the browser and
<kbd>u</kbd> undoes the move.

## Moving cards in bulk

For one card the above is the quickest way to move it. For a lot of them at
once, `hoard move` takes a holdings document on stdin and files every card in it
into one binder:

```console
$ hoard export --binder want --json | hoard move --to Binder
```

`hoard export` chooses the holdings and `hoard move` acts on them, so any
[filter](filtering.md) is also a bulk selection. [Scripting](scripting.md) goes
further, narrowing the document with `jq` between the two.
