package store

// roomActivitySQL returns the latest room or thread-message timestamp. SQLite
// stores variable-precision RFC3339Nano text: lexical MAX incorrectly ranks an
// exact second above its fractions, or .1 above .11. Use the existing index to
// find the latest second, then compare padded fractions only within that second.
func (s *SQLStore) roomActivitySQL() string {
	if s.dialect != dialectSQLite {
		return `(SELECT MAX(m.created_at) FROM messages m WHERE m.room_id = r.id)`
	}
	return `(SELECT m.created_at FROM messages m
  WHERE m.room_id = r.id AND m.created_at >= (
   SELECT substr(last.created_at, 1, 19) FROM messages last
   WHERE last.room_id = r.id ORDER BY last.created_at DESC LIMIT 1
  )
  ORDER BY substr(replace(substr(m.created_at, 21), 'Z', '') || '000000000', 1, 9) DESC
  LIMIT 1)`
}
