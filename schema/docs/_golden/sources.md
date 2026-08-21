# Reference

## Configuration

| Name | Type | Required | Default | Values | Constraints | yaml | env | Description |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| [`server`](#server) | object | yes |  |  |  | `server` |  | HTTP server settings. |
| [`storage`](#storage-type-s3) | union | yes |  |  |  | `storage` |  | Where the service keeps its data. |
| [`sites`](#sites) | list of object | no |  |  |  | `sites` |  | Served sites. |
| `level` | string | no | `"info"` | `"debug"`, `"info"` |  | `level` | `APP_LEVEL` | Log verbosity. |
| `token` | string | no | `[redacted]` |  |  | `token` | `APP_TOKEN` | API token. **Secret.** |
| `tags` | list of string | no | `[]` |  | checked by sorted; at most 8 items | `tags` | `APP_TAGS` | Tags applied to every metric. |
| `retries` | integer | no | `3` (documented) |  |  | `retries` | `APP_RETRIES` | Retry budget; unset means the client default. |
| `region` | string | no | `"eu-west-1"` |  |  | `region` | `APP_REGION` | Deployment region. |
| `verbose` | boolean | no | `false` |  |  | `verbose` | `APP_VERBOSE` | Log every request. **Deprecated.** set level to debug instead. |

## server

HTTP server settings.

| Name | Type | Required | Default | Constraints | yaml | env | Description |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `address` | string | no | `"127.0.0.1"` | non-empty; matches ^[a-z0-9.]+$ | `server.address` | `APP_SERVER_ADDRESS` | Listen address. |
| `port` | integer | no | `8080` | at least 1, at most 65535 | `server.port` | `APP_SERVER_LISTEN_PORT` | Listen port. |
| `timeout` | integer (seconds) | no | `30` | at least 1 | `server.timeout` | `APP_SERVER_TIMEOUT` | Request timeout. |

### Examples

`server.port`:

```
8080
9090
```

## storage (type: s3)

Where the service keeps its data.

Selected by `type: s3`.

| Name | Type | Required | Values | Constraints | yaml | env | Description |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `type` | string | yes | `s3` |  | `storage.type` | `APP_STORAGE_TYPE` | Selects this variant. |
| `bucket` | string | yes |  | non-empty | `storage.bucket` | `APP_STORAGE_BUCKET` |  |

## storage (type: local)

Where the service keeps its data.

Selected by `type: local`.

| Name | Type | Required | Values | Constraints | yaml | env | Description |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `type` | string | yes | `local` |  | `storage.type` | `APP_STORAGE_TYPE` | Selects this variant. |
| `path` | string | yes |  | non-empty | `storage.path` | `APP_STORAGE_PATH` |  |

## sites[]

Served sites.

| Name | Type | Required | Default | Constraints | yaml | Description |
| --- | --- | --- | --- | --- | --- | --- |
| `name` | string | yes |  | non-empty | `sites[].name` |  |
| `max_bytes` | integer | no | `1024` | at most 1048576 | `sites[].max_bytes` |  |
