package config

// The dashboard's grid is twelve tracks wide, as the web's is, and a tile is
// some number of them: `width: 6` is half the screen, `width: 4` a third.
//
// **Twelve because it divides.** Halves, thirds, quarters and sixths are all a
// whole number of tracks, so every layout a person can want in a row is a
// sentence of `width:`s that add to twelve, and the terminal's own width never
// enters into what was meant. `columns: 3` could not say that: it meant three
// of however many columns the terminal had room for, which was two on a laptop
// and four on a monitor, and `span: 2` meant two of those. The width is now a
// fraction of the screen and the screen decides only how many cells that is.
//
// **A tile is never narrower than it can be read.** A `width` that would draw a
// tile under MinTileCells at the terminal's width is read as the least that does
// (internal/render/tui), the way a page's column stacks on a phone, so one file
// holds on every terminal and the narrow ones draw it taller.
const GridTracks = 12

// MinHeight and MaxHeight bound a tile's `height:`, in lines of its box
// (borders included): under MinHeight a panel has no room for its title and a
// line, and over MaxHeight it is a page and not a tile, which is what `enter`
// opens.
const (
	MinHeight = 4
	MaxHeight = 60
)
