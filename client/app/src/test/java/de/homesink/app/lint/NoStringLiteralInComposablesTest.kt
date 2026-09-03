package de.homesink.app.lint

import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.File

/**
 * Acceptance (WP-C1): "no string literal in any composable."
 *
 * D-37 makes every user-visible string a resource. This scans the real source of
 * the `ui/` package (plus `MainActivity`) and fails if any `@Composable` function
 * body contains a string literal — the code equivalent of a lint rule, asserted
 * rather than eyeballed.
 */
class NoStringLiteralInComposablesTest {

    @Test
    fun noComposableFunctionContainsAStringLiteral() {
        val roots = listOf(
            File("src/main/java/de/homesink/app"),
            File("app/src/main/java/de/homesink/app"),
        )
        val base = roots.firstOrNull { it.isDirectory }
        if (base == null) {
            fail("could not locate app sources from ${File(".").absolutePath}")
            return
        }

        val files = buildList {
            File(base, "ui").walkTopDown().filter { it.isFile && it.extension == "kt" }.forEach(::add)
            File(base, "MainActivity.kt").takeIf { it.isFile }?.let(::add)
        }
        assertTrue("expected to scan at least the nav + theme + MainActivity sources", files.size >= 4)

        val violations = files.flatMap { file ->
            findComposableStringLiterals(file.readText()).map { (line, snippet) ->
                "${file.name}:$line  ->  $snippet"
            }
        }

        if (violations.isNotEmpty()) {
            fail(
                "string literals found inside @Composable functions " +
                    "(use a string resource, D-37):\n" + violations.joinToString("\n"),
            )
        }
    }

    /** Line number (1-based) and text of every string literal lexically inside a @Composable fun body. */
    private fun findComposableStringLiterals(source: String): List<Pair<Int, String>> {
        val composableBodies = composableFunctionBodyRanges(source)
        if (composableBodies.isEmpty()) return emptyList()

        val result = mutableListOf<Pair<Int, String>>()
        var i = 0
        var line = 1
        var inLineComment = false
        var inBlockComment = false
        val n = source.length
        while (i < n) {
            val c = source[i]
            when {
                c == '\n' -> {
                    line++
                    inLineComment = false
                    i++
                }
                inLineComment -> i++
                inBlockComment -> {
                    if (c == '*' && i + 1 < n && source[i + 1] == '/') { inBlockComment = false; i += 2 } else i++
                }
                c == '/' && i + 1 < n && source[i + 1] == '/' -> { inLineComment = true; i += 2 }
                c == '/' && i + 1 < n && source[i + 1] == '*' -> { inBlockComment = true; i += 2 }
                c == '"' -> {
                    val triple = source.startsWith("\"\"\"", i)
                    val start = i
                    val startLine = line
                    if (triple) {
                        i += 3
                        while (i < n && !source.startsWith("\"\"\"", i)) { if (source[i] == '\n') line++; i++ }
                        i += 3
                    } else {
                        i++
                        while (i < n && source[i] != '"') {
                            if (source[i] == '\\') i++
                            if (i < n && source[i] == '\n') line++
                            i++
                        }
                        i++
                    }
                    if (composableBodies.any { start in it }) {
                        result += startLine to source.substring(start, minOf(i, n)).take(60)
                    }
                }
                else -> i++
            }
        }
        return result
    }

    /** Character ranges of every `@Composable`-annotated function body, braces included. */
    private fun composableFunctionBodyRanges(source: String): List<IntRange> {
        val ranges = mutableListOf<IntRange>()
        val annotation = Regex("""@Composable\s+(?:(?:private|internal|public|inline|suspend|actual|expect|external|tailrec|operator)\s+)*fun\b""")
        for (match in annotation.findAll(source)) {
            var i = match.range.last + 1
            var parenDepth = 0
            var seenParams = false
            val n = source.length
            // Walk to the body-opening brace at paren depth 0, after the parameter list.
            while (i < n) {
                when (source[i]) {
                    '(' -> { parenDepth++; seenParams = true }
                    ')' -> parenDepth--
                    '{' -> if (parenDepth == 0 && seenParams) break
                    '=' -> if (parenDepth == 0 && seenParams) { i = -1; break } // expression body, no block
                }
                i++
            }
            if (i <= 0 || i >= n || source[i] != '{') continue
            val bodyStart = i
            var depth = 0
            var inString = false
            var inChar = false
            var inLine = false
            var inBlock = false
            var j = i
            while (j < n) {
                val c = source[j]
                when {
                    inLine -> if (c == '\n') inLine = false
                    inBlock -> if (c == '*' && j + 1 < n && source[j + 1] == '/') { inBlock = false; j++ }
                    inString -> if (c == '\\') j++ else if (c == '"') inString = false
                    inChar -> if (c == '\\') j++ else if (c == '\'') inChar = false
                    c == '/' && j + 1 < n && source[j + 1] == '/' -> { inLine = true; j++ }
                    c == '/' && j + 1 < n && source[j + 1] == '*' -> { inBlock = true; j++ }
                    c == '"' -> inString = true
                    c == '\'' -> inChar = true
                    c == '{' -> depth++
                    c == '}' -> {
                        depth--
                        if (depth == 0) { ranges += bodyStart..j; break }
                    }
                }
                j++
            }
        }
        return ranges
    }
}
