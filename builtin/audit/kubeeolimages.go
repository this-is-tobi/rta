package audit

import (
	_ "embed"
	"regexp"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Which product an image is.
//
// Recognition is the image's last path segment against the catalogue's names
// and aliases, and it stops there on purpose: a registry, an organisation or a
// mirror in front of the name says where an image came from and not what it is.
// What it cannot do is name a piece of a product for the piece. cert-manager
// ships cert-manager-controller, cert-manager-cainjector and cert-manager-webhook,
// and none of them is a catalogue name, so a third of the images on a real
// cluster had no verdict for want of a mapping nobody could give.
//
// Three tiers, in this order, all matching on the repository's tail and none
// on its registry unless the entry names one:
//
//  1. The operator's own `images` setting: `<repository suffix>=<product>`, or
//     `<repository suffix>=-` to say an image is theirs and not to be graded.
//     It is configuration, and Local, because it chooses which page of a fixed
//     host is asked about, and a caller may not choose that.
//  2. A short table built in (kubeeol-images.txt), for the pieces that version
//     in lockstep with the product.
//  3. The catalogue's own names and aliases.
//
// A suffix matches whole path segments, the longest wins within a tier, and
// case is ignored. Nothing is guessed from a name that merely starts like a
// product: that is a suggestion in the report and never a verdict.

//go:embed kubeeol-images.txt
var builtinImageTable string

// imageRule maps the images whose repository ends in suffix to a product, or
// leaves them out of the grading (exclude).
type imageRule struct {
	suffix  string
	product string
	exclude bool
	// text is the entry as the rule reads it, for the messages that name it.
	text string
}

var (
	suffixRe  = regexp.MustCompile(`^[a-z0-9._:-]+$`)
	productRe = regexp.MustCompile(`^[a-z0-9._-]+$`)
)

// parseImageEntry reads `<suffix>=<product>` or `<suffix>=-`.
func parseImageEntry(entry string) (imageRule, bool) {
	suffix, product, ok := strings.Cut(strings.TrimSpace(entry), "=")
	suffix = strings.Trim(strings.ToLower(strings.TrimSpace(suffix)), "/")
	product = strings.ToLower(strings.TrimSpace(product))
	if !ok || suffix == "" || product == "" {
		return imageRule{}, false
	}
	for _, segment := range strings.Split(suffix, "/") {
		if segment == "" || segment == "." || segment == ".." || !suffixRe.MatchString(segment) {
			return imageRule{}, false
		}
	}
	r := imageRule{suffix: suffix, text: suffix + "=" + product}
	switch {
	case product == "-":
		r.exclude = true
	case productRe.MatchString(product):
		r.product = product
	default:
		return imageRule{}, false
	}
	return r, true
}

// parseImageRules reads the operator's `images` setting and refuses the first
// entry it cannot, or a suffix given twice: two meanings for one image is a
// setting that does something other than what it says.
func parseImageRules(raw []string) ([]imageRule, *view.Error) {
	var rules []imageRule
	seen := map[string]bool{}
	for _, entry := range raw {
		r, ok := parseImageEntry(entry)
		if !ok {
			return nil, view.Errorf("audit.kube.eol.images.invalid", "images entry %q is not <repository suffix>=<product>", entry).
				WithHint("for example `ghcr.io/me/app=postgresql`, or `me/app=-` to leave an image out; " +
					"a suffix is path segments of the repository, with an optional registry in front")
		}
		if seen[r.suffix] {
			return nil, view.Errorf("audit.kube.eol.images.invalid", "images entry %q: %q is listed twice", entry, r.suffix).
				WithHint("one meaning per suffix")
		}
		seen[r.suffix] = true
		rules = append(rules, r)
	}
	return rules, nil
}

// builtinImageRules is the embedded table. A line it cannot read is skipped,
// and the test that guards the table counts them.
func builtinImageRules() []imageRule {
	var rules []imageRule
	for _, line := range strings.Split(builtinImageTable, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if r, ok := parseImageEntry(line); ok {
			rules = append(rules, r)
		}
	}
	return rules
}

// imageMap is the rules in force for one run.
type imageMap struct{ operator, builtin []imageRule }

// newImageMap checks the operator's rules against the catalogue and keeps the
// built-in ones the catalogue can back.
//
// An operator entry naming a product the site does not list is refused, with
// the entry: a mapping that silently graded nothing is the failure the setting
// exists to end. A table entry for a product the site does not list is dropped
// without a word, because the site renames a product from time to time and the
// cost of that should be a row not graded and not an error for everybody.
func newImageMap(operator []imageRule, index map[string]string) (imageMap, *view.Error) {
	var m imageMap
	for _, r := range operator {
		if !r.exclude {
			product, ok := index[r.product]
			if !ok {
				return imageMap{}, view.Errorf("audit.kube.eol.images.unknown",
					"images entry %q names %q, which endoflife.date does not list", r.text, r.product).
					WithHint("`rta eol products` lists the names and aliases it knows; `" + r.suffix + "=-` leaves the image out instead")
			}
			r.product = product
		}
		m.operator = append(m.operator, r)
	}
	for _, r := range builtinImageRules() {
		if index[r.product] == r.product {
			m.builtin = append(m.builtin, r)
		}
	}
	return m, nil
}

// resolve is the rule that applies to an image: the operator's before the
// table's, the longest suffix within each.
func (m imageMap) resolve(u *imageUse) (imageRule, bool) {
	if r, ok := longestRule(m.operator, u); ok {
		return r, true
	}
	return longestRule(m.builtin, u)
}

func longestRule(rules []imageRule, u *imageUse) (imageRule, bool) {
	var best imageRule
	var found bool
	for _, r := range rules {
		if (!found || len(r.suffix) > len(best.suffix)) && u.endsWith(r.suffix) {
			best, found = r, true
		}
	}
	return best, found
}

// endsWith reports that one of the repository's spellings ends in suffix, on a
// path-segment boundary. The registry-free form is tried and so is each
// spelling as written, so an entry may name the registry or leave it out.
func (u *imageUse) endsWith(suffix string) bool {
	if hasPathSuffix(u.repo, suffix) {
		return true
	}
	for _, full := range u.fulls {
		if hasPathSuffix(full, suffix) {
			return true
		}
	}
	return false
}

func hasPathSuffix(path, suffix string) bool {
	return path == suffix || strings.HasSuffix(path, "/"+suffix)
}

// splitImage reads a container image reference into its repository as written
// (full), the same without the registry (repo), the tag, and whether the bytes
// are pinned by digest.
//
// repo is the image's identity: two references with the same repo are the same
// project's image whichever mirror they were pulled through, and two that differ
// are not, whatever their last segment. A leading component with a dot or a
// colon, or "localhost", is a registry by Docker's own rule — a Docker Hub user
// name has neither — and a mirror in front of a registry is another. Docker
// Hub's implicit "library/" is not part of an official image's name.
func splitImage(ref string) (full, repo, tag string, digest bool) {
	ref = strings.TrimSpace(ref)
	if at := strings.Index(ref, "@"); at >= 0 {
		digest = true
		ref = ref[:at]
	}
	cut := strings.LastIndex(ref, "/") + 1
	name, tag, _ := strings.Cut(ref[cut:], ":")
	full = strings.ToLower(ref[:cut] + name)

	parts := strings.Split(full, "/")
	for len(parts) > 1 && (parts[0] == "localhost" || strings.ContainsAny(parts[0], ".:")) {
		parts = parts[1:]
	}
	if len(parts) == 2 && parts[0] == "library" {
		parts = parts[1:]
	}
	return full, strings.Join(parts, "/"), tag, digest
}
