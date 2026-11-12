package note

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/this-is-tobi/rta/builtin/internal/itemstore"
)

// rootlessByScan is how a listing answered before the store was read once:
// every parent found by a scan of the store. Kept as the reference the one
// read is held to.
func rootlessByScan(s itemstore.Store, it itemstore.Item) bool {
	p := it.Parent
	for steps := 0; p != 0; steps++ {
		i, ok := index(s, p)
		if !ok || steps > len(s.Items) {
			return true
		}
		p = s.Items[i].Parent
	}
	return false
}

// One read of the store answers what a scan per row answered, for every note
// of stores holding what a store can come to hold: cycles, a note that is its
// own parent, a parent that is gone, and an id written twice.
func TestOneReadOfTheStoreAnswersWhatAScanPerRowDid(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for round := range 200 {
		n := 1 + r.IntN(40)
		s := itemstore.Store{}
		for i := range n {
			id := 1 + i
			if r.IntN(10) == 0 {
				id = 1 + r.IntN(n) // an id written twice
			}
			parent := 0
			if r.IntN(3) > 0 {
				parent = r.IntN(n + 5) // up to a few ids that are not there
			}
			s.Items = append(s.Items, itemstore.Item{ID: id, Parent: parent, Done: r.IntN(2) == 0})
		}
		tr := readTree(s)
		for _, it := range s.Items {
			if got, want := tr.rootless(s, it), rootlessByScan(s, it); got != want {
				t.Fatalf("round %d, note %d: rootless %v, want %v", round, it.ID, got, want)
			}
			done, total := itemstore.Progress(s, it.ID)
			if got := tr.progress[it.ID]; got != [2]int{done, total} {
				t.Fatalf("round %d, note %d: progress %v, want %d/%d", round, it.ID, got, done, total)
			}
		}
	}
}

// What a listing costs as the store grows: linear, where it was the square.
func BenchmarkReadingTheTree(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		s := itemstore.Store{}
		for i := range n {
			parent := 0
			if i%5 == 0 && i > 0 {
				parent = i
			}
			s.Items = append(s.Items, itemstore.Item{ID: i + 1, Parent: parent})
		}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for b.Loop() {
				tr := readTree(s)
				for _, it := range s.Items {
					_ = tr.rootless(s, it)
					_ = tr.progress[it.ID]
				}
			}
		})
	}
}
