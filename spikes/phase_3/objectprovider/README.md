# Spike: SeaweedFS as the local object provider (#350)

Unmaintained spike scripts; see CLAUDE.md, *Spikes And Deferred Work*. The
findings are in `docs/v2/phase_3/spike_local-object-provider.md`.

- `derive/` prints the object-store credentials the plane derives from a
  root-of-trust key under `$MAESTRO_HOME`, minting the key if absent.
- `seaweedfs/compose.yaml` runs SeaweedFS the way the plane runs its object
  provider: one bind-mounted data directory, the invoking uid, credentials by
  environment, one loopback port.
- `probe/` starts one multipart upload and prints what `minio-go` parses from
  `ListMultipartUploads`, which is how the missing `Initiated` was found (the
  raw XML was read with `aws --debug s3api list-multipart-uploads`).

Run the existing adapter suite against it, unchanged:

```sh
export MAESTRO_HOME=/tmp/spike-home
eval "$(go run ./derive)"; export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY
MAESTRO_UID=$(id -u) MAESTRO_GID=$(id -g) SPIKE_S3_PORT=58333 SPIKE_DATA_DIR=/tmp/spike-data \
  docker compose -f seaweedfs/compose.yaml up -d
(cd ../../.. && MAESTRO_OBJECTS_PORT=58333 go test -tags=integration -count=1 -v ./internal/dataplane/objects/)
```
