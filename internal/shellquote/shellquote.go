// Package shellquote spells a value as one word of a command a person is
// shown and pastes into a shell: the grant a refusal says to issue, the
// `rta dashboard add` that puts a removed tile back. Such a command is built
// by joining words, and a value holding a space, a ';' or a '$(' then makes a
// different command from the one it reads as — a stray argument, or a second
// command that runs on paste.
package shellquote

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Arg returns s as one shell word that reads back as s, byte for byte.
//
// Bare when every character is one no shell treats specially, which covers
// what rta itself generates — a capability, a profile, key=value with a
// plain value — so the common command reads the way it always has. In single
// quotes otherwise, a single quote spliced in as '"'"'.
//
// In $'...' when s holds a character a terminal does not draw as itself: a
// control, an invisible or reordering character, a byte that is not UTF-8.
// Single quotes would carry it exactly, but the command is printed before it
// is pasted, and the renderer cleans what it prints — an escape sequence in a
// debug.ansi tile's input showed as nothing, and the pasted command recreated
// a different tile. $'...' spells each such character as the octal escapes
// of its bytes, three digits each, which the shell turns back into them: bash
// (3.2 included), zsh and ksh read them, as does POSIX sh since its 2024
// edition. Not \x: ksh93 reads every hex digit that follows one, so an escape
// followed by a letter from a to f became another character.
//
// An empty s is returned empty: a caller builds a command by leaving an unset
// part out, not by passing an empty word.
func Arg(s string) string {
	switch {
	case s == "" || bare(s):
		return s
	case !utf8.ValidString(s) || strings.ContainsFunc(s, unseen):
		return dollarQuoted(s)
	default:
		return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
	}
}

// bare reports whether s is one word as it stands. '=' only past the first
// character: zsh expands a word that begins with one into a command's path.
func bare(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:@%,", r):
		case r == '=' && i > 0:
		default:
			return false
		}
	}
	return true
}

// unseen reports a character a terminal does not draw as itself. The ASCII
// space is the one blank unicode.IsPrint counts; every other space, a
// no-break space included, looks like one and is not.
func unseen(r rune) bool { return !unicode.IsPrint(r) }

func dollarQuoted(s string) string {
	var b strings.Builder
	b.WriteString("$'")
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '\\' || r == '\'':
			b.WriteByte('\\')
			b.WriteByte(s[i])
		case (r == utf8.RuneError && size == 1) || unseen(r):
			for _, c := range []byte(s[i : i+size]) {
				fmt.Fprintf(&b, `\%03o`, c)
			}
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	b.WriteByte('\'')
	return b.String()
}
