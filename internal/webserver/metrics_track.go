package webserver

// trackWSConnection increments honey_ws_connections_active for kind until the returned func runs.
func (s *Server) trackWSConnection(kind string) func() {
	if s.metrics == nil {
		return func() {}
	}
	s.metrics.IncWS(kind)
	return func() { s.metrics.DecWS(kind) }
}
