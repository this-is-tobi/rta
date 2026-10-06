package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The configuration is one file and, beside it, a directory of more.
//
// **Why a directory beside the file, and not an `include:` in it.** A line in
// the file that names a path is a line that can be pointed anywhere: at a file
// a repository ships, at one another account can write. A directory whose name
// is the file's own (`config.yaml` has `config.d`) names nothing: it is where
// the person who owns the file put more of it, trusted exactly as far as the
// file is, and absent exactly when the file is not honoured. The working-
// directory fallback has none, for the reason it has no profiles and no
// dashboard: a cloned repository does not get to arrange what rta connects to.
//
// **Each unit is stated in one file.** A unit is what a person means by "that
// profile": `profiles.<name>`, `roles.<name>`, one plugin's settings
// (`plugins.<key>`), the `dashboard:` block, the `theme:` block, `output`.
// Stating one twice is refused, naming both files, and not resolved by an order
// nobody wrote down. The alternative — a later file wins — is how a profile
// called prod quietly points somewhere else for every command that follows, and
// it is the one thing here a person could not see from either file alone. It
// costs nothing the split is for: a team's profiles in one file, an
// environment's in another, the dashboard and the colours in a third.
//
// **A write goes where the unit lives.** Every writer hands back the whole
// configuration it was given, changed (Mutate); this splits it back by owner,
// so `rta profile set prod` edits the file that holds prod and `rta dashboard
// add` the one that holds the dashboard, and a unit nothing holds yet is
// written to the file itself. A drop-in is never created by rta and never
// rewritten for what in it did not change, since render keeps every block it
// was not asked to touch.

// maxDropIns bounds what a directory can make every command read. Far beyond
// what anyone splits a configuration into, and short of what a directory
// filled to be slow would be.
const maxDropIns = 64

// DropInDir is the directory whose files are read after the config file, or ""
// when none is: the config file's own name with `.d` for its extension —
// `config.d` beside `config.yaml`, `rta.d` beside the file RTA_CONFIG names.
//
// Empty for the working-directory fallback, which is not a file anybody named
// (trustedPath).
func DropInDir() string {
	if !trustedPath() {
		return ""
	}
	path := Path()
	base := filepath.Base(path)
	return filepath.Join(filepath.Dir(path), strings.TrimSuffix(base, filepath.Ext(base))+".d")
}

// File is one file the configuration is read from.
type File struct {
	Path string
	// Main is the config file itself, which is listed whether or not it exists.
	Main bool
	// Exists is whether the file is there to read.
	Exists bool
	// Units are what it states, as Unit spells them.
	Units []string
}

// layer is a file as it was read: its text, so a write can keep what a person
// typed, and what it parsed to.
type layer struct {
	path   string
	main   bool
	exists bool
	text   []byte
	cfg    Config
}

// stack is the files in the order they are read — the config file, then each
// drop-in by name — and which of them states each unit.
type stack struct {
	layers []layer
	owner  map[string]int
}

// dropInNames is the files of the drop-in directory that are read: regular
// files, or links that resolve to one, named *.yaml or *.yml, in the order of
// their names. A name starting with a dot or ending with a tilde is an editor's
// or a backup's and is not configuration, and neither is anything else —
// a README, a .bak, a directory.
func dropInNames(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	switch {
	case os.IsNotExist(err):
		return nil, nil
	case err != nil:
		return nil, view.Errorf("config.unreadable", "reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
			continue
		}
		if ext := filepath.Ext(name); ext != ".yaml" && ext != ".yml" {
			continue
		}
		// Stat follows a link, which is what a dotfiles manager makes of this
		// directory, and only a regular file is read: a named pipe or a device
		// here would hold every command.
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || !info.Mode().IsRegular() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > maxDropIns {
		return nil, view.Errorf("config.toomany", "%s holds %d configuration files, and rta reads at most %d",
			dir, len(names), maxDropIns).
			WithHint("merge some of them: a file can hold as many profiles as you like")
	}
	return names, nil
}

// readStack reads every file of the configuration and refuses a unit stated
// twice. Nothing of the environment is folded in, as LoadFile never does.
func readStack() (*stack, error) { return readStackWith("", nil, Config{}, false) }

// readStackWith is readStack with the file at path taken to hold text instead
// of what is on disk — the text an editor is about to save — and added to the
// stack when nothing is there yet. An empty path takes nothing from the caller.
//
// Lenient leaves a file that does not parse out of the stack instead of
// refusing it: the question being asked is about one file, and another's
// parse error is that file's to report, once, under its own name.
func readStackWith(path string, text []byte, cfg Config, lenient bool) (*stack, error) {
	mainText, err := ReadText()
	if err != nil {
		return nil, err
	}
	mainCfg, err := parse(Path(), mainText)
	if err != nil && (!lenient || path == Path()) {
		return nil, err
	}
	s := &stack{layers: []layer{{path: Path(), main: true, exists: mainText != nil, text: mainText, cfg: mainCfg}}}
	dir := DropInDir()
	names, err := dropInNames(dir)
	if err != nil {
		return nil, err
	}
	replaced := false
	for _, name := range names {
		file := filepath.Join(dir, name)
		fileText, err := readFile(file)
		if err != nil {
			return nil, view.Errorf("config.unreadable", "reading %s: %v", file, err)
		}
		fileCfg, err := parse(file, fileText)
		if err != nil && (!lenient || file == path) {
			return nil, err
		}
		if file == path {
			fileText, fileCfg, replaced = text, cfg, true
		}
		s.layers = append(s.layers, layer{path: file, exists: true, text: fileText, cfg: fileCfg})
	}
	switch {
	case path == Path():
		s.layers[0].text, s.layers[0].cfg = text, cfg
	case path != "" && !replaced:
		s.layers = append(s.layers, layer{path: path, text: text, cfg: cfg})
	}
	if err := s.index(); err != nil {
		return nil, err
	}
	return s, nil
}

// index says which file states each unit, and refuses a unit stated twice.
func (s *stack) index() error {
	s.owner = map[string]int{}
	for i, l := range s.layers {
		for _, unit := range unitsOf(l.cfg) {
			if first, taken := s.owner[unit]; taken {
				return view.Errorf("config.duplicate", "%s is stated in both %s and %s",
					describeUnit(unit), s.layers[first].path, l.path).
					WithHint("a profile, a role, a plugin's settings and each of the blocks lives in one " +
						"file: keep it in whichever you like and take it out of the other")
			}
			s.owner[unit] = i
		}
	}
	return nil
}

// merged is the configuration the files state together.
func (s *stack) merged() Config {
	out := Config{trusted: s.layers[0].cfg.trusted}
	var base Dashboard
	views := map[string]View{}
	for _, l := range s.layers {
		c := l.cfg
		if c.Output != "" {
			out.Output = c.Output
		}
		if dashboardSet(c.Dashboard) {
			base = c.Dashboard
		}
		for name, v := range c.Dashboard.Views {
			views[name] = v
		}
		if len(c.Theme) > 0 {
			out.Theme = c.Theme
		}
		for k, v := range c.Plugins {
			if out.Plugins == nil {
				out.Plugins = map[string]map[string]any{}
			}
			out.Plugins[k] = v
		}
		for k, v := range c.Profiles {
			if out.Profiles == nil {
				out.Profiles = map[string]Profile{}
			}
			out.Profiles[k] = v
		}
		for k, v := range c.Roles {
			if out.Roles == nil {
				out.Roles = map[string]Role{}
			}
			out.Roles[k] = v
		}
	}
	out.Dashboard = base
	out.Dashboard.Views = nil
	if len(views) > 0 {
		out.Dashboard.Views = views
	}
	return out
}

// split is next, a changed merged configuration, cut back into what each file
// states: a unit goes to the file that stated it, and one that nothing stated
// goes to the config file. A unit that is gone from next is gone from its file.
func (s *stack) split(next Config) []Config {
	parts := make([]Config, len(s.layers))
	for i, l := range s.layers {
		parts[i].trusted = l.cfg.trusted
	}
	place := func(unit string) int {
		if i, ok := s.owner[unit]; ok {
			return i
		}
		return 0
	}
	if next.Output != "" {
		parts[place("output")].Output = next.Output
	}
	if dashboardSet(next.Dashboard) {
		base := next.Dashboard
		base.Views = nil
		parts[place("dashboard")].Dashboard = base
	}
	for name, v := range next.Dashboard.Views {
		p := &parts[place("views/"+name)]
		if p.Dashboard.Views == nil {
			p.Dashboard.Views = map[string]View{}
		}
		p.Dashboard.Views[name] = v
	}
	if len(next.Theme) > 0 {
		parts[place("theme")].Theme = next.Theme
	}
	for k, v := range next.Plugins {
		p := &parts[place("plugins/"+k)]
		if p.Plugins == nil {
			p.Plugins = map[string]map[string]any{}
		}
		p.Plugins[k] = v
	}
	for k, v := range next.Profiles {
		p := &parts[place("profiles/"+k)]
		if p.Profiles == nil {
			p.Profiles = map[string]Profile{}
		}
		p.Profiles[k] = v
	}
	for k, v := range next.Roles {
		p := &parts[place("roles/"+k)]
		if p.Roles == nil {
			p.Roles = map[string]Role{}
		}
		p.Roles[k] = v
	}
	return parts
}

// writeBack writes what changed in next to the files that state it. A file
// whose units are as they were is rendered to its own text and left alone.
func (s *stack) writeBack(next Config) error {
	parts := s.split(next)
	// The config file last: if a drop-in cannot be written, the file that every
	// command reads first is the one that did not change.
	for i := len(parts) - 1; i >= 0; i-- {
		l := s.layers[i]
		data, err := render(l.text, parts[i])
		if err != nil {
			return writeErr(l.path, err)
		}
		if err := persist(l.path, l.text, data); err != nil {
			return err
		}
		// The schema its header names goes beside a config file this write
		// created. A drop-in is never created here, so it never has one.
		if l.main && len(bytes.TrimSpace(l.text)) == 0 && bytes.Contains(data, []byte("$schema="+SchemaFile)) {
			ensureSchemaFile(filepath.Dir(l.path))
		}
	}
	return nil
}

// writeErr is a render failure as the error it is to a caller: a *view.Error
// as it came, anything else as an encoding failure.
func writeErr(path string, err error) error {
	var verr *view.Error
	if errors.As(err, &verr) {
		return verr
	}
	return view.Errorf("config.encode", "encoding %s: %v", path, err)
}

// dashboardSet reports whether a dashboard block states anything of its own: the
// views it holds are units of their own, each in one file.
func dashboardSet(d Dashboard) bool {
	return len(d.Tiles)+len(d.Add)+len(d.Hidden)+len(d.Order) > 0 || d.Columns != 0
}

// unitsOf is what a configuration states, one name per unit: `output`,
// `dashboard`, `theme`, `profiles/<name>`, `roles/<name>`, `plugins/<key>`.
func unitsOf(c Config) []string {
	var units []string
	if c.Output != "" {
		units = append(units, "output")
	}
	if dashboardSet(c.Dashboard) {
		units = append(units, "dashboard")
	}
	if len(c.Theme) > 0 {
		units = append(units, "theme")
	}
	for k := range c.Plugins {
		units = append(units, "plugins/"+k)
	}
	for k := range c.Profiles {
		units = append(units, "profiles/"+k)
	}
	for k := range c.Roles {
		units = append(units, "roles/"+k)
	}
	for k := range c.Dashboard.Views {
		units = append(units, "views/"+k)
	}
	sort.Strings(units)
	return units
}

// describeUnit is a unit as a sentence names it.
func describeUnit(unit string) string {
	kind, name, named := strings.Cut(unit, "/")
	if !named {
		switch kind {
		case "output":
			return "`output`"
		default:
			return "the `" + kind + ":` block"
		}
	}
	switch kind {
	case "profiles":
		return "profile " + name
	case "roles":
		return "role " + name
	case "views":
		return "dashboard view " + name
	default:
		return fmt.Sprintf("the settings of plugin %s", name)
	}
}

// FilePaths is every file the configuration is read from that is there, the
// config file first, without reading any of them: what a check needs to read
// each and report its own faults, including a file that does not parse.
func FilePaths() ([]string, error) {
	var out []string
	if _, err := os.Stat(Path()); err == nil {
		out = append(out, Path())
	}
	dir := DropInDir()
	names, err := dropInNames(dir)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		out = append(out, filepath.Join(dir, name))
	}
	return out, nil
}

// Files is every file the configuration is read from — the config file, then
// each drop-in by name — and what each states. It is what `rta config` shows
// and what a person asks of a split configuration: which file holds that?
func Files() ([]File, error) {
	s, err := readStack()
	if err != nil {
		return nil, err
	}
	out := make([]File, len(s.layers))
	for i, l := range s.layers {
		out[i] = File{Path: l.path, Main: l.main, Exists: l.exists, Units: unitsOf(l.cfg)}
	}
	return out, nil
}

// Where is the file that states a unit — a profile, a role, a plugin's
// settings, or one of the blocks, spelled kind and name (`profiles`, `prod`;
// `dashboard`, ""). The config file when nothing states it yet, which is where
// a write puts it.
func Where(kind, name string) string { return Owners()(kind, name) }

// Owners is Where for a caller that asks about many units: the files are read
// once and every answer comes from that reading.
func Owners() func(kind, name string) string {
	s, err := readStack()
	return func(kind, name string) string {
		unit := kind
		if name != "" {
			unit = kind + "/" + name
		}
		if err != nil {
			return Path()
		}
		if i, ok := s.owner[unit]; ok {
			return s.layers[i].path
		}
		return Path()
	}
}

// DropInFile is the path a drop-in of this name has, or an error when name is
// not one the directory reads: a name only, never a path, so that asking for a
// drop-in cannot name a file outside the directory.
func DropInFile(name string) (string, error) {
	dir := DropInDir()
	if dir == "" {
		return "", view.Errorf("config.nodropins", "%s is not a configuration file somebody named, so it has no drop-ins", Path())
	}
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", view.Errorf("config.dropin", "%q is not a file name", name).
			WithHint("name a file in " + dir + ", without a directory")
	}
	if ext := filepath.Ext(name); ext != ".yaml" && ext != ".yml" {
		name += ".yaml"
	}
	return filepath.Join(dir, name), nil
}
