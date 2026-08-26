# Filtering

Press <kbd>/</kbd> in the browser to narrow what you are looking at. That
command line gives you the same query as `hoard export --filter`, so anything
you work out interactively can be pasted straight into a script.

```console
$ hoard export --binder Binder --filter 'price<1 rarity:common'
```

## Keys

| Key             | Matches                                | Example                       |
| --------------- | -------------------------------------- | ----------------------------- |
| *(bare word)*   | card name, anywhere in it              | `sol ring`, `"lion's eye"`    |
| `name`          | card name, anywhere in it              | `name:ulamog`                 |
| `set`           | set code                               | `set:uma`                     |
| `finish`        | `nonfoil`, `foil` or `etched`          | `finish:foil`                 |
| `board`         | `main`, `side`, `commander` or `maybe` | `board:side`                  |
| `qty`           | copies held                            | `qty>=4`                      |
| `price`         | price per copy, USD                    | `price<1`                     |
| `value`         | copies × price, USD                    | `value>=20`                   |
| `cmc`           | mana value                             | `cmc<=2`                      |
| `rarity`        | the whole rarity                       | `rarity:mythic`               |
| `type`, `t`     | type line                              | `t:creature`                  |
| `artist`        | artist                                 | `artist:guay`                 |
| `layout`        | Scryfall layout                        | `layout:transform`            |
| `setname`       | full set name                          | `setname:"modern horizons"`   |
| `color`, `c`    | colour identity letters                | `c:wu`                        |

## How terms combine

Terms are ANDed, so repeating a key makes a range: `price>=5 price<20`. The
comparisons `>` `>=` `<` `<=` work on the numeric keys; everything else takes
`:` or `=`. Quote anything with a space in it.

Text matching ignores case and matches anywhere in the field. An exception is
`rarity`, `finish` and `board`, which must match whole. `c:wu` asks for a colour
identity containing both W and U, not one that is exactly WU.

Because terms are ANDed, `set:` cannot name two sets at once. To sweep several
sets in one pass, export each and splice the documents together. See
[Scripting](scripting.md).
