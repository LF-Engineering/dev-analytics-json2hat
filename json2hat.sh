#!/bin/bash
if [ -z "$1" ]
then
  echo "Please specify env as a 1st arg: prod|test|local"
  exit 1
fi
env="${1}"
if [ -z "${ES_URL}" ]
then
  export ES_URL="`cat ./secrets/ES_URL.${env}.secret`"
fi
if [ -z "${SH_DSN}" ]
then
  export SH_DSN="`cat ./secrets/SH_DSN.${env}.secret`"
fi
if [ -z "${SYNC_URL}" ]
then
  export SYNC_URL="`cat ./secrets/SYNC_URL.${env}.secret`"
fi
export REPO_ACCESS="`cat ./secrets/REPO_ACCESS.secret`"
export NO_PROFILE_UPDATE=1
export REPLACE=1
export ONLY_GITHUB=1
# export DRY_RUN=1
# export SKIP_BOTS=1
# export NAME_MATCH=0|1|2
export NAME_MATCH=1
./json2hat
