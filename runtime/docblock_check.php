<?php

/**
 * Runtime helper injected by docblockcheck.
 * Verifies array element types declared in @param / @return docblocks and logs
 * every mismatch to a TSV file. Include this once, e.g. from PHPUnit bootstrap.
 *
 * Log path: env DOCBLOCK_CHECK_LOG, else <cwd>/docblock-check.log
 * Row format: file \t line \t context \t expected \t actual \t sample
 */

if (!function_exists('__docblock_check')) {

    function __docblock_check($values, string $expected, string $file, int $line, string $context): void
    {
        $log = getenv('DOCBLOCK_CHECK_LOG') ?: (getcwd() . '/docblock-check.log');

        $record = static function (string $actual, $sample) use ($log, $file, $line, $context, $expected): void {
            $s = is_scalar($sample) ? (string) $sample : __docblock_typename($sample);
            $s = str_replace(["\t", "\n", "\r"], ' ', $s);
            if (strlen($s) > 40) {
                $s = substr($s, 0, 40) . '...';
            }
            $row = implode("\t", [$file, (string) $line, $context, $expected, $actual, $s]) . "\n";
            @file_put_contents($log, $row, FILE_APPEND | LOCK_EX);
        };

        // Only plain arrays are inspected. Generators and other Iterators are
        // skipped: iterating them here would consume the caller's value.
        if (!is_array($values)) {
            return;
        }

        foreach ($values as $v) {
            if (!__docblock_matches($v, $expected)) {
                $record(__docblock_typename($v), $v);
            }
        }
    }

    function __docblock_matches($v, string $t): bool
    {
        switch (strtolower($t)) {
            case 'string':
                return is_string($v);
            case 'int':
            case 'integer':
                return is_int($v);
            case 'float':
            case 'double':
                return is_float($v);
            case 'bool':
            case 'boolean':
                return is_bool($v);
            case 'array':
                return is_array($v);
            case 'callable':
                return is_callable($v);
            case 'object':
                return is_object($v);
            case 'iterable':
                return is_iterable($v);
            case 'scalar':
                return is_scalar($v);
            case 'mixed':
            case 'null':
            case 'void':
            case '':
                return true;
        }

        $cls = ltrim($t, '\\');
        // Unknown type (e.g. a @template generic param) - cannot verify, skip.
        if (!class_exists($cls) && !interface_exists($cls) && !enum_exists($cls)) {
            return true;
        }
        return $v instanceof $cls;
    }

    function __docblock_typename($v): string
    {
        return is_object($v) ? get_class($v) : gettype($v);
    }
}
