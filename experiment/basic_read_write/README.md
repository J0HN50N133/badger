# basic_read_write

A minimal experiment binary for basic Badger write/read validation.

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
  "dir": "/path/to/badger-lsm",
  "valueDir": "/path/to/badger-vlog",
  "evictionPolicy": "fifo",
  "keepLocalClosed": 2,
  "pruneLocal": true
}
```

`dir` and `valueDir` are optional. If omitted, the experiment uses a temporary directory.
If only one of `dir` / `valueDir` is provided, the other is set to the same value.
`evictionPolicy` is optional and must be one of `fifo`, `lru`, `lfu`.
`s3Endpoint` and `s3Bucket` are required for S3 object-store injection.

## Run

```bash
go run ./experiment/basic_read_write ./config.json
```

## Notes
- All experiment parameters are loaded from the JSON file.
- Current required parameters: `s3Endpoint`, `s3Bucket`.
- Optional parameters: `s3Prefix`, `s3Region`, `s3UsePathStyle`, `dir`, `valueDir`, `evictionPolicy`, `keepLocalClosed`, `pruneLocal`.
- `s3Endpoint` is currently used as experiment metadata output and embedded in the written value.
