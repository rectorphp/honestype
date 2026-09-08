# docblockcheck

Spots invalid `@param`/`@return` array types (`string[]`, `int[]`, `Foo[]`, ...)
by checking them against real runtime values while your unit tests run, then
renders the differences - optionally as a Checkstyle report for PR annotations.

Built on [rectorphp/php-parser-in-go](https://github.com/rectorphp/php-parser-in-go).

## How it works

1. **instrument** parses each `.php` file and injects a tiny type check for every
   single-dimension array docblock type:
   - `@param T[] $x` -> a check at the top of the function body
   - `@return T[]` -> each `return` in that scope is wrapped and its value checked
   Class types are emitted as `Name::class`, so they resolve against the file's
   namespace and `use` imports. The runtime helper `docblock_check.php` is written
   next to the sources.
2. You run your **unit tests**. Every mismatch is appended to a TSV log
   (`./docblock-check.log`, or `$DOCBLOCK_CHECK_LOG`).
3. **report** deduplicates the log, prints a summary, and can emit Checkstyle XML.
4. Restore the sources with `git checkout` once the log is collected.

The instrumentation only observes - it never changes behaviour. A file already
containing `__docblock_check(` is skipped, so re-running is safe.

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
runs the pipeline and feeds the Checkstyle report to
[reviewdog](https://github.com/reviewdog/reviewdog), which posts each mismatch as
an inline annotation on the changed lines.

## Scope and limits

- Only single-dimension trailing-`[]` types are handled (`Foo[]`, `?int[]`).
  Multi-dimension (`int[][]`), generics (`array<int, string>`) and unions
  (`int|string[]`) are ignored.
- `@return` inside nested closures/arrow functions is not attributed to the
  outer function's docblock.
- A class type that does not resolve to a real class/interface/enum (e.g. a
  `@template` generic param such as `TEnum[]`) is skipped, not reported.
- `int` vs `float` is strict: an `int` passed where `float[]` is declared is
  reported, matching PHP's own `is_float`.
- Parsed as PHP 8.3.
