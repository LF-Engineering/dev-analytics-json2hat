# json2hat

Import company affiliations from cncf/devstats into GrimoireLab Sorting Hat database.

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

To cleanup existing company affiliations (delete from `organizations` and `enrollments` tables) set the `SH_CLEANUP` variable.

Testing connection:

- `SH_TEST_CONNECT` - set this variable to only test connection.


# Affiliations JSON path

`json2hat` needs to read `cncf/devstats` affiliations json file. It first tries to read a local json file and fallbacks to a remote file.

You can set local file path via `SH_LOCAL_JSON_PATH=/path/to/github_users.json`. Default value is `github_users.json`. If local file is found then no remote file is read.

You can set remote file path via `SH_REMOTE_JSON_PATH=http://some.url.org/path/to/github_users.json`. Default value is `https://github.com/cncf/devstats/raw/master/github_users.json`. This file is only read when reading local json fails. If both local and remote files cannot be read program exists with a fatal error message.


# Company acquisitions YAML path

`json2hat` needs to read `cncf/devstats` company acquisitions/name mapping yaml file. It first tries to read a local json file and fallbacks to a remote file.

You can set local file path via `SH_LOCAL_YAML_PATH=/path/to/companies.yaml`. Default value is `companies.yaml`. If local file is found then no remote file is read.

You can set remote file path via `SH_REMOTE_YAML_PATH=http://some.url.org/path/to/companies.yaml`. Default value is `https://github.com/cncf/devstats/raw/master/companies.yaml`. This file is only read when reading local json fails. If both local and remote files cannot be read program exists with a fatal error message.


# DA company names mapping

`json2hat` reads [this file](https://github.com/LF-Engineering/dev-analytics-affiliation/raw/master/map_org_names.yaml) for mappings.


# Docker

`json2hat` is packaged as a docker image [docker.io/dajohn/json2hat](https://cloud.docker.com/u/dajohn/repository/docker/dajohn/json2hat). You can use scripts from `docker/` directory to manage docker image.

Scripts (most require setting docker username via something like this: `docker login; DOCKER_USER=your_user_name ./docker/docker_scriptname.sh`):

- `docker/docker_build.sh` - this will build `json2hat` docker image. Image is using multi layer setup to build the smallest possible output. It don't even have `bash`. See `Dockerfile` for details. Image is only about 6Mb size.
- `docker/docker_run.sh` - this will execute `json2hat` from within the container. You should pass `SH_*` variables to control Sorting Hat database connection and affiliations JSON path.
- `docker/docker_publish.sh` - it will publish `json2hat` image to your docker hub.
- `docker/docker_pull.sh` - it will pull `json2hat` image from your docker hub.
- `docker/docker_remove.sh` - removes generated `json2hat` docker image.
- `docker/docker_cleanup.sh` - removes generated `json2hat` docker image and executes `docker system prune`.

# Running locally

- Replace env with `prod` or `test` or `local`: `./json2hat.sh env`.
- Pass `ONLY_GGH_USERNAME=1` if you want to match username only for git and GitHub source.
- Pass `ONLY_GGH_NAME=1` if you want to match name only for git and GitHub source.
- Clear `NO_PROFILE_UPDATE` env if you do not want import to be able to update country and other profile data.
- Pass `REPLACE=1` env if you want to replace any existing affiliations found (will only touch affiliations with `project_slug` like `cncf/*` or `cncf-f`).
- Pass `DRY_RUN=1` to avoid and DB writing.
- Pass `SKIP_BOTS=1` to avoid auto marking bots.
- Pass `ONLY_GGH_USERNAME=1` to match usernames only for git or GitHub usernames.
- Pass `ONLY_GGH_NAME=1` to match names only for git or GitHub names.
- Use `NAME_MATCH=n` to specify how to match using name: 0 - do not match using name, 1 - match only when single hit, 2 - match on multiple hits, default is 1.
- Set `ORGS_RO=1` to skip adding any new organizations. It will dump a CSV file with missing org names then and won't add any enrollments to orgs that were not found (directly, lowerace or by acquisition or mapping YAMLs).
- Set `MISSING_ORGS_CSV=filename.csv` to specify filename containing missing orgs (only when `ORGS_RO` is used), default is `missing.csv` if not specified.


# Company names mapping

You should call DA affiliations API `map_org_names` after a successfull CNCF affiliations data import.

