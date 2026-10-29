package renderer

// OccupySlots takes n concurrency slots, so that tests can hit the limit deterministically. The
// returned function frees them again.
func (s *Server) OccupySlots(n int) (release func()) {
	for range n {
		s.sem <- struct{}{}
	}

	return func() {
		for range n {
			<-s.sem
		}
	}
}
