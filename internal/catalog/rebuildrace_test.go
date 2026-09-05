package catalog

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestSearchingWhileTheCatalogRebuildsStaysSafe(t *testing.T) {
	c := stocked(t)

	serveBundle(t, "2026-08-01T00:00:00Z", []string{
		card("opt", "Opt", "eld", "59", "0.25"),
		card("sol1", "Sol Ring", "c21", "263", "2.00"),
		card("bit", "Bitterblossom", "uma", "85", "34.11"),
		card("tar", "Tarmogoyf", "mm3", "144", "18.00"),
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := c.Autocomplete(context.Background(), "sol"); err != nil {
					if strings.Contains(err.Error(), "database is closed") {
						t.Errorf("the add cascade lost the catalog mid-rebuild: %v", err)
						return
					}
					t.Errorf("Autocomplete during a rebuild: %v", err)
					return
				}
			}
		}()
	}

	err := c.Update(context.Background(), nil)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if _, err := c.Autocomplete(context.Background(), "sol"); err != nil {
		t.Errorf("Autocomplete after the rebuild: %v", err)
	}
}
