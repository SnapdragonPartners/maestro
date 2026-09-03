-- The work hierarchy rows (ADR 0018, migrations 000003 and 000021) and the
-- reads a dispatch basis is assembled from (Phase 3 item 3, design D10).
--
-- Lineage is DERIVED by the seam from the parent row, never supplied by the
-- caller: an Epic's product is its Feature's, a Story's feature and product
-- are its Epic's. The composite foreign keys make a contradiction
-- unrepresentable; deriving makes it unspellable.

-- name: InsertFeature :one
INSERT INTO features (feature_id, organization_id, user_id, product_id, title, is_wrapper)
VALUES (@feature_id, @organization_id, @user_id, @product_id, @title, @is_wrapper)
RETURNING *;

-- name: GetFeature :one
SELECT * FROM features WHERE organization_id = $1 AND feature_id = $2;

-- name: InsertEpic :one
INSERT INTO epics (epic_id, organization_id, user_id, product_id, feature_id, repository_id, title)
VALUES (@epic_id, @organization_id, @user_id, @product_id, @feature_id, @repository_id, @title)
RETURNING *;

-- name: GetEpic :one
SELECT * FROM epics WHERE organization_id = $1 AND epic_id = $2;

-- Locks the Epic row: the stable parent every Story-graph write, pointer
-- repoint and dispatch creation serializes on (ADR 0027; design D10).
-- name: LockEpic :one
SELECT * FROM epics WHERE organization_id = $1 AND epic_id = $2 FOR UPDATE;

-- name: InsertStory :one
INSERT INTO stories (story_id, organization_id, user_id, product_id, feature_id, epic_id, title)
VALUES (@story_id, @organization_id, @user_id, @product_id, @feature_id, @epic_id, @title)
RETURNING *;

-- name: GetStory :one
SELECT * FROM stories WHERE organization_id = $1 AND story_id = $2;

-- name: ListStoriesByEpic :many
SELECT * FROM stories WHERE organization_id = $1 AND epic_id = $2 ORDER BY story_id;

-- One Work Group per Epic (000021's one-per-epic key is the arbiter).
-- name: InsertWorkGroupIfAbsent :execrows
INSERT INTO work_groups (work_group_id, organization_id, product_id, feature_id, epic_id)
VALUES (@work_group_id, @organization_id, @product_id, @feature_id, @epic_id)
ON CONFLICT ON CONSTRAINT work_groups_one_per_epic_key DO NOTHING;

-- name: GetWorkGroupByEpic :one
SELECT * FROM work_groups WHERE organization_id = $1 AND epic_id = $2;

-- The incoming edges of one Story, with the completion that currently
-- satisfies each. Read under the Epic lock when assembling a basis.
-- name: ListIncomingStoryDependencies :many
SELECT predecessor_story_id, satisfying_completion_artifact_id, satisfying_completion_is_amendment, created_at
FROM story_dependencies
WHERE organization_id = $1 AND epic_id = $2 AND successor_story_id = $3
ORDER BY predecessor_story_id;
