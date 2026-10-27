package vm

// ShootdownFix (default off) closes the race between a TLB shootdown and a
// translation that is in flight when the shootdown happens: a miss waiting in
// a TLB MSHR (and every request merged onto it), or a filled entry still
// being returned to the requesters. Without it, the shootdown only drops the
// entries already in the TLB, and a fill that returns afterwards installs and
// returns the translation read before the remap.
//
// When on, InvalidateInflight marks the in-flight miss of the page as stale;
// the fill then takes the current page-table entry instead of the one it
// carries (the re-walk is not charged extra latency: the walk that is already
// in flight pays it). Entries that are being returned are refreshed at once.
type ShootdownFix struct {
	On      bool
	Refresh func(pid PID, vAddr uint64) (Page, bool)

	stale map[shootdownKey]struct{}

	// Marked counts the in-flight misses marked stale; Refreshed counts the
	// fills and returning entries that took the current page-table entry.
	Marked, Refreshed uint64
}

type shootdownKey struct {
	pid   PID
	vAddr uint64
}

// Enable turns the fix on with the page-table lookup used for refreshes.
func (s *ShootdownFix) Enable(refresh func(pid PID, vAddr uint64) (Page, bool)) {
	s.On = true
	s.Refresh = refresh
}

// Mark records that the in-flight miss of the page (pid, vAddr) is stale.
func (s *ShootdownFix) Mark(pid PID, vAddr uint64) {
	if s.stale == nil {
		s.stale = map[shootdownKey]struct{}{}
	}
	s.stale[shootdownKey{pid, vAddr}] = struct{}{}
	s.Marked++
}

// Fresh returns the current page-table entry of page p.
func (s *ShootdownFix) Fresh(p Page) Page {
	if s.Refresh == nil {
		return p
	}
	if f, ok := s.Refresh(p.PID, p.VAddr); ok {
		s.Refreshed++
		return f
	}
	return p
}

// FixFill returns the page a fill should install and return: the current
// page-table entry if the miss was marked stale, else p.
func (s *ShootdownFix) FixFill(p Page) Page {
	if !s.On || len(s.stale) == 0 {
		return p
	}
	k := shootdownKey{p.PID, p.VAddr}
	if _, ok := s.stale[k]; !ok {
		return p
	}
	delete(s.stale, k)
	return s.Fresh(p)
}
