# Honestype

Finds `@param`/`@return` iterable types that lie (`string[]`, `Foo[]`,
`array<int, Foo>`, `Collection<Foo>`, `int[][]`, ...). It checks them against the
real values flowing through your code while your unit tests run, then prints
exactly which docblock is wrong and where.

Built on [rectorphp/php-parser-in-go](https://github.com/rectorphp/php-parser-in-go).

## Quick start

```bash
go build -o docblockcheck .

# 1. instrument your sources in place (one or more paths)
./docblockcheck instrument src/

# 2. load the runtime helper in tests/bootstrap.php:
#    require __DIR__ . '/../src/docblock_check.php';

# 3. run your tests - mismatches get logged to ./docblock-check.log
vendor/bin/phpunit

# 4. print the report (non-zero exit if anything is wrong)
./docblockcheck report docblock-check.log

# 5. restore your sources
git checkout -- src/ && rm -f src/docblock_check.php docblock-check.log
```

The instrumentation only observes - it never changes behaviour, and re-running is
safe. `vendor`, `node_modules`, `.git`, `Fixture`, `Fixtures` and `Source`
directories are skipped.

## Example output

A method declares `@param Node[]` but a caller passes an array of strings:

```
 ---- ------------------------------------------------------------------
  Line   src/NodeProcessor.php
 ---- ------------------------------------------------------------------
  42     param $nodes: expected PhpParser\Node, found 'string' (e.g. foo)
 ---- ------------------------------------------------------------------

 src/NodeProcessor.php:42  in NodeProcessor::process()
   param $nodes: expected PhpParser\Node, found 'string' (e.g. foo)
   passed by NodeProcessorTest::testProcess() (tests/NodeProcessorTest.php:31)

     39 |     /**
     40 |      * @param Node[] $nodes
     41 |      */
   > 42 |     public function process(array $nodes): array
     43 |     {
     44 |         foreach ($nodes as $node) {

 [ERROR] Found 1 docblock type mismatch(es)
```

How to read it: `param $nodes` is the offending docblock, `expected
PhpParser\Node` is what `@param Node[]` promised, `found 'string'` is the real
element type, `e.g. foo` is a sample value. Line 42 is the method, and `passed
by` is the exact test method - and line - that fed in the wrong value.

**Fix:** correct the docblock to the real type (`@param string[] $nodes`), or
fix the caller that passes the wrong values. Then re-run steps 3-4.

Hit a false positive? Drop it with `-skip 'Class::method()'` (repeatable):

```bash
./docblockcheck report -skip 'NodeProcessor::process()' docblock-check.log
```

## CI: PR annotations

Add `-github` to print `::warning file=...,line=...` commands, so each mismatch
shows as an inline annotation on the PR - no external action needed. A
ready-to-copy workflow is in `.github/workflows/docblock-check.yml.sample`.

## Scope and limits

- Unions beyond `X|null` (e.g. `Foo|Bar`) are skipped, to avoid false positives.
- `@return` inside nested closures/arrow functions is not attributed to the outer
  docblock.
- A class type that resolves to no real class/interface/enum (e.g. a `@template`
  generic like `TEnum[]`) is skipped, not reported.
- `array` and `IteratorAggregate` values (Laravel `Collection`, `ArrayObject`)
  are traversed; a `Generator` or one-shot `Iterator` is skipped, since iterating
  it here would consume the caller's value.
- `int` vs `float` is strict, matching PHP's own `is_float`.
- Parsed as PHP 8.3.
