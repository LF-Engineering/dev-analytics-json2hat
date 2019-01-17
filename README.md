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

Testing connection:

- `SH_TEST_CONNECT` - set this variable to only test connection.


# Affiliations JSON path

`json2hat` needs to read `cncf/gitdm` affiliations json file. It first tries to read a local json file and fallbacks to a remote file.

You can set local file path via `SH_LOCAL_JSON_PATH=/path/to/github_users.json`. Default value is `github_users.json`. If local file is found then no remote file is read.

You can set remote file path via `SH_REMOTE_JSON_PATH=http://some.url.org/path/to/github_users.json`. Default value is `https://raw.githubusercontent.com/cncf/gitdm/master/github_users.json`. This file is only read when reading local json fails. If both local and remote files cannot be read program exists with a fatal error message.

# Docker

`json2hat` is packaged as a docker image [docker.io/lukaszgryglicki/json2hat](https://cloud.docker.com/u/lukaszgryglicki/repository/docker/lukaszgryglicki/json2hat). You can use scripts from `docker/` directory to manage docker image.

Scripts (most require setting docker username via something like this: `docker login; DOCKER_USER=your_user_name ./docker/docker_scriptname.sh`):

- `docker/docker_build.sh` - this will build `json2hat` docker image. Image is using multi layer setup to build the smallest possible output. It don't even have `bash`. See `Dockerfile` for details. Image is only about 6Mb size.
- `docker/docker_run.sh` - this will execute `json2hat` from within the container. You should pass `SH_*` variables to control Sorting Hata database connection and affiliations JSON path.
- `docker/docker_publish.sh` - it will publish `json2hat` image to your docker hub.
- `docker/docker_pull.sh` - it will pull `json2hat` image from your docker hub.
- `docker/docker_remove.sh` - removes generated `json2hat` docker image.
- `docker/docker_cleanup.sh` - removes generated `json2hat` docker image and executes `docker system prune`.
