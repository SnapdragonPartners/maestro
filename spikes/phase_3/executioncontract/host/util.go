package host

// slicesContains exists only so a mutation can express union-composition of
// scopes; nothing in the boundary calls it.
func slicesContains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
