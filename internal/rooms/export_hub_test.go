package rooms

// SetSyncPageBytesForTest lowers the sync page budget; call the result to
// restore it.
func SetSyncPageBytesForTest(limit int) func() {
	previous := syncPageBytes
	syncPageBytes = limit
	return func() { syncPageBytes = previous }
}

// SyncHubRoomOnceForTest runs one catch-up pass (what the sync loop runs),
// without the control-log pull SyncHubRoomContext does first.
func (s *Service) SyncHubRoomOnceForTest(globalRoomID string) error {
	hub, ok, err := s.hubRoomByGlobal(s.lifecycleCtx, globalRoomID)
	if err != nil || !ok {
		return err
	}
	return s.syncHubRoom(s.lifecycleCtx, hub)
}
