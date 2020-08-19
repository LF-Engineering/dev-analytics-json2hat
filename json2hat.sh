#!/bin/bash
if [ -z "$1" ]
then
  echo "Please specify env as a 1st arg: prod|test|local"
  exit 1
fi
env="${1}"
export ES_URL="`cat ./secrets/ES_URL.${env}.secret`"
export SH_DSN="`cat ./secrets/SH_DSN.${env}.secret`"
# export SH_DSN="`cat ./secrets/SH_DSN.local.secret`"
export SYNC_URL="`cat ./secrets/SYNC_URL.${env}.secret`"
export REPO_ACCESS="`cat ./secrets/REPO_ACCESS.secret`"
export NO_PROFILE_UPDATE=1
export REPLACE=1
./json2hat
