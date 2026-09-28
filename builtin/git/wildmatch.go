package git

import "strings"

// wildmatch is git's own pattern matcher (wildmatch.c), which it matches ignore
// patterns with, ported line for line: whether text matches pattern, read as
// git reads it under flags.
//
// **go-git matched ignore patterns with filepath.Match, which is not git's
// matcher.** It reads `[!a]` as a class holding `!` and `a`, where git negates
// it, so `.env[!.]*` kept out no file of git's and git.status listed, and
// git.diff showed whole, a .envrc git ignores. It refuses `[]a]` and `[a-]`,
// which git reads as classes holding `]` and `-`, so a pattern holding one
// ignored nothing; it has no `[[:upper:]]`; and a `**` next to anything but
// a slash matched nothing there, where git reads it as `*`. Every one of
// those is a file git ignores, a secret among them, that this listed and
// showed. So ignore patterns are matched here, with git's matcher, and read
// as git reads them (ignorePattern).
//
// A byte past the end of either string reads as the NUL that ends a C string:
// neither holds one, since a line of an ignore file ends at its first NUL
// (parseIgnore) and a path never holds one.
//
// b is the status's budget, which each step of the matching counts against,
// and which a pattern of many stars over a long path spends: the matching
// stops where it runs out, and matches nothing from there (statusBudget).
func wildmatch(pattern, text string, flags wmFlags, b *statusBudget) bool {
	return dowild(pattern, text, flags, b) == wmMatch
}

// wmFlags are git's WM_ flags: wmPathname, where * and ? match no slash and
// ** matches across them, and wmCasefold, where a letter of the text matches
// either case.
type wmFlags uint8

const (
	wmPathname wmFlags = 1 << iota
	wmCasefold
)

// dowild's results, as git's names them: a match, no match here, and no
// match anywhere further on, at all or short of a **.
const (
	wmMatch = iota
	wmNoMatch
	wmAbortAll
	wmAbortToStarStar
)

// at is s[i], or the NUL that ends a C string past its end.
func at(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func dowild(pattern, text string, flags wmFlags, b *statusBudget) int {
	if b.tick() {
		return wmAbortAll
	}
	fold := flags&wmCasefold != 0
	p, t := 0, 0
	for ; p < len(pattern); p, t = p+1, t+1 {
		pCh, tCh := pattern[p], at(text, t)
		if tCh == 0 && pCh != '*' {
			return wmAbortAll
		}
		if fold {
			tCh, pCh = lowerASCII(tCh), lowerASCII(pCh)
		}
		switch pCh {
		case '\\':
			// The byte after it, as it is: a letter escaped keeps its case.
			p++
			if tCh != at(pattern, p) {
				return wmNoMatch
			}
		case '?':
			if flags&wmPathname != 0 && tCh == '/' {
				return wmNoMatch
			}
		case '*':
			var matchSlash bool
			if p++; at(pattern, p) == '*' {
				prev := p
				for p++; at(pattern, p) == '*'; p++ {
				}
				switch {
				case flags&wmPathname == 0:
					matchSlash = true
				case (prev < 2 || pattern[prev-2] == '/') &&
					(at(pattern, p) == 0 || at(pattern, p) == '/' || at(pattern, p) == '\\' && at(pattern, p+1) == '/'):
					// A ** between slashes matches nothing too: foo/**/bar
					// matches foo/bar.
					if at(pattern, p) == '/' && dowild(pattern[p+1:], text[t:], flags, b) == wmMatch {
						return wmMatch
					}
					matchSlash = true
				}
			} else {
				matchSlash = flags&wmPathname == 0
			}
			switch {
			case p == len(pattern):
				// A trailing ** matches everything, a trailing * what
				// holds no more slashes.
				if !matchSlash && strings.IndexByte(text[t:], '/') >= 0 {
					return wmAbortToStarStar
				}
				return wmMatch
			case !matchSlash && pattern[p] == '/':
				// One * then a slash matches the rest of one directory's
				// name; the slash is matched by the loop.
				slash := strings.IndexByte(text[t:], '/')
				if slash < 0 {
					return wmAbortAll
				}
				t += slash
				continue
			}
			for tCh != 0 {
				// A literal after the * is looked for first: what comes
				// before it belongs to the *, and past a slash where the *
				// matches none it is not there to find.
				if !isGlobSpecial(pattern[p]) {
					want := pattern[p]
					if fold {
						want = lowerASCII(want)
					}
					for tCh = at(text, t); tCh != 0 && (matchSlash || tCh != '/'); tCh = at(text, t) {
						if fold {
							tCh = lowerASCII(tCh)
						}
						if tCh == want {
							break
						}
						t++
					}
					if tCh != want {
						if matchSlash {
							return wmAbortAll
						}
						return wmAbortToStarStar
					}
				}
				if matched := dowild(pattern[p:], text[t:], flags, b); matched != wmNoMatch {
					if !matchSlash || matched != wmAbortToStarStar {
						return matched
					}
				} else if !matchSlash && tCh == '/' {
					return wmAbortToStarStar
				}
				t++
				tCh = at(text, t)
			}
			return wmAbortAll
		case '[':
			n, matched, ok := matchClass(pattern[p:], tCh, fold)
			if !ok {
				return wmAbortAll
			}
			p += n - 1
			if !matched || flags&wmPathname != 0 && tCh == '/' {
				return wmNoMatch
			}
		default:
			if tCh != pCh {
				return wmNoMatch
			}
		}
	}
	if t < len(text) {
		return wmNoMatch
	}
	return wmMatch
}

// matchClass is whether the bracket expression class starts with matches c,
// and how many bytes of class it takes, its ] included, as git's wildmatch
// reads one: ! or ^ first negates it, a ] or - first or a - last is itself,
// a backslash escapes the byte after it, and [:name:] is a POSIX class. ok
// is false where it is never closed or names no class git knows, which
// matches no text at all. c is lowered where fold is set, and a member is
// not: an upper-case letter in one matches no text then, as in git.
func matchClass(class string, c byte, fold bool) (n int, matched, ok bool) {
	p := 1
	pCh := at(class, p)
	if pCh == '^' {
		pCh = '!'
	}
	negated := pCh == '!'
	if negated {
		p++
		pCh = at(class, p)
	}
	var prev byte
	for {
		if pCh == 0 {
			return 0, false, false
		}
		switch {
		case pCh == '\\':
			p++
			if pCh = at(class, p); pCh == 0 {
				return 0, false, false
			}
			matched = matched || c == pCh
		case pCh == '-' && prev != 0 && at(class, p+1) != 0 && at(class, p+1) != ']':
			p++
			if pCh = class[p]; pCh == '\\' {
				p++
				if pCh = at(class, p); pCh == 0 {
					return 0, false, false
				}
			}
			switch {
			case c <= pCh && c >= prev:
				matched = true
			case fold && isLowerASCII(c):
				if up := c &^ 0x20; up <= pCh && up >= prev {
					matched = true
				}
			}
			pCh = 0
		case pCh == '[' && at(class, p+1) == ':':
			start := p + 2
			end := start
			for at(class, end) != 0 && class[end] != ']' {
				end++
			}
			if at(class, end) == 0 {
				return 0, false, false
			}
			if end-start < 1 || class[end-1] != ':' {
				// No :] before the ], so the [ is a member like any other.
				matched = matched || c == '['
				break
			}
			in, known := posixClass(class[start:end-1], c, fold)
			if !known {
				return 0, false, false
			}
			matched = matched || in
			p, pCh = end, 0
		default:
			matched = matched || c == pCh
		}
		prev = pCh
		p++
		if pCh = at(class, p); pCh == ']' {
			return p + 1, matched != negated, true
		}
	}
}

// posixClass is whether c is in the POSIX class name, as git's own ctype
// has it (sane_ctype), which knows ASCII alone; known is false for a name
// it has no class for. With fold set, [:upper:] matches a lower-case letter
// too, the one class git folds.
func posixClass(name string, c byte, fold bool) (in, known bool) {
	switch name {
	case "alnum":
		return isLetter(c) || isDigit(c), true
	case "alpha":
		return isLetter(c), true
	case "blank":
		return c == ' ' || c == '\t', true
	case "cntrl":
		return c < 0x20 || c == 0x7f, true
	case "digit":
		return isDigit(c), true
	case "graph":
		return c > 0x20 && c < 0x7f, true
	case "lower":
		return isLowerASCII(c), true
	case "print":
		return c >= 0x20 && c < 0x7f, true
	case "punct":
		return c > 0x20 && c < 0x7f && !isLetter(c) && !isDigit(c), true
	case "space":
		// git's isspace, which a vertical tab and a form feed are not (gitSpace).
		return isSpace(c), true
	case "upper":
		return isUpperASCII(c) || fold && isLowerASCII(c), true
	case "xdigit":
		return isDigit(c) || c|0x20 >= 'a' && c|0x20 <= 'f', true
	}
	return false, false
}

// isGlobSpecial is a byte git's wildmatch reads as more than itself.
func isGlobSpecial(c byte) bool { return c == '*' || c == '?' || c == '[' || c == '\\' }

func isDigit(c byte) bool      { return c >= '0' && c <= '9' }
func isUpperASCII(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLowerASCII(c byte) bool { return c >= 'a' && c <= 'z' }
func lowerASCII(c byte) byte {
	if isUpperASCII(c) {
		return c | 0x20
	}
	return c
}
