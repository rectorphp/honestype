<?php

/**
 * Runtime helper injected by docblockcheck.
 * Verifies array/collection element (and key) types declared in @param /
 * @return docblocks and logs every mismatch to a TSV file. Include this once,
 * e.g. from PHPUnit bootstrap.
 *
 * Log path: env DOCBLOCK_CHECK_LOG, else <cwd>/docblock-check.log
 * Row format: file \t line \t context \t expected \t actual \t sample
 *
 * $desc is a descriptor built by the instrumenter:
 *   leaf:      ['t' => 'string']   or ['t' => Foo::class, 'null' => true]
 *   container: ['v' => <desc>]      (+ 'k' => 'string', + 'c' => Coll::class)
 */

if (!function_exists('__docblock_check')) {

    function __docblock_check($value, array $desc, string $file, int $line, string $context): void
    {
        $errors = [];
        __docblock_match($value, $desc, $context, $errors);
        if ($errors === []) {
            return;
        }

        $log = getenv('DOCBLOCK_CHECK_LOG') ?: (getcwd() . '/docblock-check.log');
        $rows = '';
        foreach ($errors as $e) {
            $sample = str_replace(["\t", "\n", "\r"], ' ', $e['sample']);
            if (strlen($sample) > 40) {
                $sample = substr($sample, 0, 40) . '...';
            }
            $rows .= implode("\t", [$file, (string) $line, $e['ctx'], $e['exp'], $e['act'], $sample]) . "\n";
        }
        @file_put_contents($log, $rows, FILE_APPEND | LOCK_EX);
    }

    function __docblock_match($value, array $desc, string $path, array &$errors): void
    {
        // Leaf: a single expected type.
        if (isset($desc['t'])) {
            if ($value === null && !empty($desc['null'])) {
                return;
            }
            if (!__docblock_leaf_ok($value, $desc['t'])) {
                $exp = $desc['t'] . (!empty($desc['null']) ? '|null' : '');
                $errors[] = ['ctx' => $path, 'exp' => $exp, 'act' => __docblock_typename($value), 'sample' => __docblock_sample($value)];
            }
            return;
        }

        // Container: optional class, then key/value element checks.
        if (isset($desc['c'])) {
            $c = ltrim((string) $desc['c'], '\\');
            if ((class_exists($c) || interface_exists($c)) && !($value instanceof $c)) {
                $errors[] = ['ctx' => $path, 'exp' => $desc['c'], 'act' => __docblock_typename($value), 'sample' => __docblock_sample($value)];
                return;
            }
        }

        // Only traverse values that can be read without being consumed.
        // Arrays and IteratorAggregate (Collection, ArrayObject, ...) are safe;
        // a Generator or one-shot Iterator is skipped.
        if (!is_array($value) && !($value instanceof \IteratorAggregate && !($value instanceof \Generator))) {
            return;
        }

        foreach ($value as $k => $element) {
            if (isset($desc['k']) && !__docblock_key_ok($k, $desc['k'])) {
                $errors[] = ['ctx' => $path . ' key', 'exp' => $desc['k'], 'act' => gettype($k), 'sample' => (string) $k];
            }
            __docblock_match($element, $desc['v'], $path . '[' . $k . ']', $errors);
        }
    }

    function __docblock_leaf_ok($v, string $t): bool
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

    function __docblock_key_ok($k, string $t): bool
    {
        switch ($t) {
            case 'string':
                return is_string($k);
            case 'int':
                return is_int($k);
            default: // array-key: string|int, always satisfied
                return true;
        }
    }

    function __docblock_typename($v): string
    {
        return is_object($v) ? get_class($v) : gettype($v);
    }

    function __docblock_sample($v): string
    {
        return is_scalar($v) ? (string) $v : __docblock_typename($v);
    }
}
