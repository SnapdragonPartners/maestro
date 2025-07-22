package git

// MergeResult represents the result of a git merge operation.
type MergeResult struct {
	Status       string
	ConflictInfo string
	MergeCommit  string
}
