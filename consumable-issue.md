# Consumable `qpu` Issue

## Summary

Tried to make `qpu` consumable while keeping it as a backend-name selector (`STRING`).
The scheduler rejects this model.

## What Was Tested

1. `qpu` as `STRING`, `Relop ==`, `Consumable YES`
- Error:
  - `Consumable "qpu" can have only <= as an relational operator`

2. `qpu` as `STRING`, `Relop <=`, `Consumable YES`
- Error:
  - `Complex "qpu" of type "STRING" cannot be a consumable`

## Conclusion

In this OCS/Gridware environment, `STRING` complexes cannot be consumable.
So `qpu` cannot be both:
- a string backend selector (`qpu=test_eagle`)
- and a consumable resource.

## Working Configuration

Keep `qpu` as:

```text
qpu qpu STRING == YES NO NONE 1000
```

This supports backend selection via `-l qpu=<backend>`, but not consumable accounting on that same `STRING` complex.
