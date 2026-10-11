package store

import "time"

// SetNow injects domain time before serving requests. Migrations, connection
// timeouts and health deadlines intentionally remain on the real clock.
func (s *SQLStore) SetNow(now func() (time.Time, error)) { s.now = now }
