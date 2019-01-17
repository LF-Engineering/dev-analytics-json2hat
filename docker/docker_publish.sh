#!/bin/bash
if [ -z "${DOCKER_USER}" ]
then
  echo "$0: you need to set docker user via DOCKER_USER=username"
  exit 1
fi
docker tag json2hat "${DOCKER_USER}/json2hat"
docker push "${DOCKER_USER}/json2hat"
