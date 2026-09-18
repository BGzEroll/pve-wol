package registry

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"pve-wol/internal/pve"
)

type Entry struct {
	Guest      pve.Guest
	Conflict   bool
	Candidates []pve.Guest
}

type Conflict struct {
	MAC        string
	Candidates []pve.Guest
}

type Stats struct {
	Guests int
	MACs   int
}

type Registry struct {
	mu      sync.RWMutex
	entries map[string]Entry
	stats   Stats
}

func New() *Registry {
	return &Registry{entries: make(map[string]Entry)}
}

func (r *Registry) Replace(guests []pve.Guest) ([]Conflict, Stats) {
	candidates := make(map[string][]pve.Guest)
	for _, guest := range guests {
		for _, mac := range guest.MACs {
			if containsGuest(candidates[mac], guest) {
				continue
			}
			candidates[mac] = append(candidates[mac], guest)
		}
	}

	entries := make(map[string]Entry, len(candidates))
	conflicts := make([]Conflict, 0)
	macs := make([]string, 0, len(candidates))
	for mac := range candidates {
		macs = append(macs, mac)
	}
	sort.Strings(macs)
	for _, mac := range macs {
		items := candidates[mac]
		if len(items) == 1 {
			entries[mac] = Entry{Guest: items[0]}
			continue
		}

		sort.Slice(items, func(i, j int) bool {
			return guestKey(items[i]) < guestKey(items[j])
		})
		entries[mac] = Entry{Conflict: true, Candidates: append([]pve.Guest(nil), items...)}
		conflicts = append(conflicts, Conflict{MAC: mac, Candidates: append([]pve.Guest(nil), items...)})
	}

	stats := Stats{Guests: len(guests), MACs: len(entries)}
	r.mu.Lock()
	r.entries = entries
	r.stats = stats
	r.mu.Unlock()
	return conflicts, stats
}

func (r *Registry) Lookup(mac string) (Entry, bool) {
	r.mu.RLock()
	entry, ok := r.entries[mac]
	r.mu.RUnlock()
	return entry, ok
}

func (r *Registry) MarkRunning(guest pve.Guest) {
	key := guestKey(guest)
	r.mu.Lock()
	defer r.mu.Unlock()
	for mac, entry := range r.entries {
		if entry.Conflict || guestKey(entry.Guest) != key {
			continue
		}
		entry.Guest.Status = "running"
		r.entries[mac] = entry
	}
}

func (r *Registry) Stats() Stats {
	r.mu.RLock()
	stats := r.stats
	r.mu.RUnlock()
	return stats
}

func containsGuest(guests []pve.Guest, candidate pve.Guest) bool {
	key := guestKey(candidate)
	for _, guest := range guests {
		if guestKey(guest) == key {
			return true
		}
	}
	return false
}

func guestKey(guest pve.Guest) string {
	return fmt.Sprintf("%s/%d@%s", strings.ToLower(guest.Type), guest.VMID, guest.Node)
}
