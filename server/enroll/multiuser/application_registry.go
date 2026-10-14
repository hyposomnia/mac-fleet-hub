package multiuser

// User performs a single indexed owner lookup for scoped application requests.
func (server *Server) User(id int64) (User, error) { return server.user(id) }
