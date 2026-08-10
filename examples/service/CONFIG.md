# Service configuration

## Configuration

| Name | Type | Required | Default | Values | yaml | env | Description |
| --- | --- | --- | --- | --- | --- | --- | --- |
| [`server`](#server) | object | yes |  |  | `server` |  | HTTP server settings. |
| [`storage`](#storage-type-s3) | union | yes |  |  | `storage` |  | Where the service keeps its data. |
| `level` | string | no | `"info"` | `"debug"`, `"info"`, `"warn"`, `"error"` | `level` | `APP_LEVEL` | Log verbosity. |
| `tags` | list of string | no | `[]` |  | `tags` | `APP_TAGS` | Tags applied to every metric. |
| `limits` | map of string to integer | no | `{}` |  | `limits` | `APP_LIMITS` | Resource limits, merged per entry. |

## server

HTTP server settings.

| Name | Type | Required | Default | Constraints | yaml | env | Description |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `address` | string | no | `"127.0.0.1"` | non-empty | `server.address` | `APP_SERVER_ADDRESS` | Listen address. |
| `port` | integer | no | `8080` | at least 1, at most 65535 | `server.port` | `APP_SERVER_LISTEN_PORT` | Listen port. |
| `timeout` | duration | no |  | at least 1ms | `server.timeout` | `APP_SERVER_TIMEOUT` | Request timeout; unset means no timeout. |

## storage (type: s3)

Where the service keeps its data.

Selected by `type: s3`.

| Name | Type | Required | Default | Values | Constraints | yaml | env | Description |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `type` | string | yes |  | `s3` |  | `storage.type` | `APP_STORAGE_TYPE` | Selects this variant. |
| `bucket` | string | yes |  |  | non-empty | `storage.bucket` | `APP_STORAGE_BUCKET` |  |
| `region` | string | no | `"us-east-1"` |  |  | `storage.region` | `APP_STORAGE_REGION` |  |
| `prefix` | string | no |  |  |  | `storage.prefix` | `APP_STORAGE_PREFIX` | Key prefix within the bucket. |

## storage (type: local)

Where the service keeps its data.

Selected by `type: local`.

| Name | Type | Required | Values | Constraints | yaml | env | Description |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `type` | string | yes | `local` |  | `storage.type` | `APP_STORAGE_TYPE` | Selects this variant. |
| `path` | string | yes |  | non-empty | `storage.path` | `APP_STORAGE_PATH` |  |
