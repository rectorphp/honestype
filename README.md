# docblockcheck

Spots invalid `@param`/`@return` iterable types (`string[]`, `Foo[]`,
`array<int, Foo>`, `Collection<Foo>`, `int[][]`, ...) by checking them against
real runtime values while your unit tests run, then renders the differences - as
a console table with source snippets, a Checkstyle report, and/or GitHub PR
annotations.

Built on [rectorphp/php-parser-in-go](https://github.com/rectorphp/php-parser-in-go).

## How it works

1. **instrument** parses each `.php` file and injects a tiny type check for every
   iterable docblock type:
   - `@param T[] $x` -> a check at the top of the function body
   - `@return T[]` -> each `return` in that scope is wrapped and its value checked
   Supported type syntax: `T[]`, `T[][]` (nested), `array<V>` / `list<V>` /
   `iterable<V>`, `array<K, V>` (key checked too), `SomeCollection<V>` /
   `SomeCollection<K, V>` (container asserted, then elements), and `X|null`.
   Class types are emitted as `Name::class`, so they resolve against the file's
   namespace and `use` imports. The runtime helper `docblock_check.php` is written
   next to the sources.
2. You run your **unit tests**. Every mismatch is appended to a TSV log
   (`./docblock-check.log`, or `$DOCBLOCK_CHECK_LOG`).
3. **report** deduplicates the log and prints the findings PHPStan-style
   (grouped per file, with a `Line` column); it can also emit Checkstyle XML
   (`-checkstyle`) and/or GitHub Actions annotations (`-github`).
4. Restore the sources with `git checkout` once the log is collected.

The instrumentation only observes - it never changes behaviour. A file already
containing `__docblock_check(` is skipped, so re-running is safe.

## What the instrumentation looks like

For each iterable `@param`/`@return`, a guarded check is injected - at the top of
the body for a parameter, and wrapped around each `return` for the return type.
The check is a no-op unless the runtime helper is loaded, so behaviour never
changes.

```diff
 /**
  * @param Node[] $nodes
  * @return Node[]
  */
 public function process(array $nodes): array
 {
+    if (\function_exists('__docblock_check')) \__docblock_check($nodes, ['v' => ['t' => Node::class]], __FILE__, 3, 'param $nodes');
     foreach ($nodes as $node) {
         $node->process();
     }

-    return $nodes;
+    { $__dbr = $nodes; if (\function_exists('__docblock_check')) \__docblock_check($__dbr, ['v' => ['t' => Node::class]], __FILE__, 4, 'return'); return $__dbr; }
 }
```

The second argument is a descriptor built from the docblock type: `Node[]` becomes
`['v' => ['t' => Node::class]]`, `array<string, Foo>` becomes
`['k' => 'string', 'v' => ['t' => Foo::class]]`, and `int[][]` becomes
`['v' => ['v' => ['t' => 'int']]]`. Class types are emitted as `Name::class` so
they resolve against the file's namespace and `use` imports.

## Usage

```bash
go build -o docblockcheck .

# 1. instrument in place
./docblockcheck instrument src/

# 2. make PHPUnit load the helper, e.g. in tests/bootstrap.php:
#    require __DIR__ . '/../src/docblock_check.php';
#    (or set auto_prepend_file to it)

# 3. run tests (fills ./docblock-check.log)
vendor/bin/phpunit

# 4. render
./docblockcheck report -checkstyle checkstyle.xml docblock-check.log

# 5. clean up
git checkout -- src/ && rm -f src/docblock_check.php docblock-check.log
```

## PR annotations (GitHub Actions)

A ready-to-copy workflow is in `.github/workflows/docblock-check.yml.sample`. It
runs the pipeline and calls `report -github`, which prints
`::warning file=...,line=...` workflow commands so each mismatch shows as an
inline annotation on the file and line - no external action required.

## Scope and limits

- Union element types beyond `X|null` (e.g. `Foo|Bar`) are skipped, to avoid
  false positives.
- `@return` inside nested closures/arrow functions is not attributed to the
  outer function's docblock.
- A class type that does not resolve to a real class/interface/enum (e.g. a
  `@template` generic param such as `TEnum[]`) is skipped, not reported.
- `array` and `IteratorAggregate` values (Laravel `Collection`, `ArrayObject`,
  ...) are traversed; a `Generator` or one-shot `Iterator` is skipped, since
  iterating it here would consume the caller's value.
- `int` vs `float` is strict: an `int` passed where `float[]` is declared is
  reported, matching PHP's own `is_float`.
- Parsed as PHP 8.3.
