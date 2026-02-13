# basic_read_write

A minimal experiment binary for basic Badger write/read validation with value-log
object storage enabled.

## Config
Pass a JSON file path as the only CLI argument.

Example `config.json`:

```json
{
  "s3Endpoint": "http://127.0.0.1:9000",
  "s3Bucket": "badger-vlog",
  "s3Prefix": "experiments/basic-rw",
  "s3Region": "us-east-1",
  "s3UsePathStyle": true,
  "minioAccessKey": "minioadmin",
  "minioSecretKey": "minioadmin",
  "dir": "/path/to/badger-lsm",
  "valueDir": "/path/to/badger-vlog",
  "evictionPolicy": "fifo",
  "keepLocalClosed": 2,
  "pruneLocal": true
}
```

## Run

```bash
go run ./experiment/basic_read_write ./config.json
```

Preferred workflow:

```bash
make -C experiment minio-s3-start
make -C experiment basic-read-write CONFIG=experiment/basic_read_write/config.example.json
make -C experiment minio-s3-stop
# optional cleanup:
# make -C experiment minio-s3-rm
```

## Notes
- All experiment parameters are loaded from JSON.
- Required parameters: `s3Endpoint`, `s3Bucket`.
- MinIO credentials come from config: `minioAccessKey`, `minioSecretKey`.
- The experiment writes 3 keys and relies on the configured eviction policy for automatic offload.
