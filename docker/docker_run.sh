#!/bin/bash
if [ -z "${DOCKER_USER}" ]
then
  echo "$0: you need to set docker user via DOCKER_USER=username"
  exit 1
fi
docker run --env-file <(env | grep SH_) "${DOCKER_USER}/json2hat" json2hat
