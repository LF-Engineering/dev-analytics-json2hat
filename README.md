# json2hat

Import company affiliations from cncf/gitdm into GrimoireLab Sorting Hat database.

# Environment parameters

Setting Sorting Hat database parameters: you can either provide full database connect string/dsn via `SH_DSN=...` or provide all or some paramaters individually, via `SH_*` environment variables. `SH_DSN=..` has a higher priority and no `SH_*` parameters are used if `SH_DSN` is provided. When using `SH_*` parameters, only `SH_PASS` is required, all other parameters have default values.

Sorting Hat database connection parameters:
- `SH_DSN` - provides full database connect string, for example: `SH_DSN='shuser:shpassword@tcp(shhost:shport)/shdb?charset=utf8'`
- `SH_USER` - user name, defaults to `shuser`.
- `SH_PASS` - password - required.
- `SH_PROTO` - protocol, defaults to `tcp`.
- `SH_HOST` - host, defaults to `localhost`.
- `SH_PORT` - port, defaults to `3306`.
- `SH_DB` - database name, defaults to `shdb`.
- `SH_PARAMS` - additional parameters that can be specified via `?param1=value1&param2=value2&...&paramN=valueN`, defaults to `?charset=utf8`. You can use `SH_PARAMS='-'` to specify empty params.


# Affiliations JSON path

`json2hat` needs to read `cncf/gitdm` affiliations json file. It first tries to read a local json file and fallbacks to a remote file.

You can set local file path via `SH_LOCAL_JSON_PATH=/path/to/github_users.json`. Default value is `github_users.json`. If local file is found then no remote file is read.

You can set remote file path via `SH_REMOTE_JSON_PATH=http://some.url.org/path/to/github_users.json`. Default value is `https://raw.githubusercontent.com/cncf/gitdm/master/github_users.json`. This file is only read when reading local json fails. If both local and remote files cannot be read program exists with a fatal error message.
