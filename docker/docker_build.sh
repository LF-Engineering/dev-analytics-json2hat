#!/bin/bash
if [ -z "${DOCKER_USER}" ]
then
  echo "$0: you need to set docker user via DOCKER_USER=username"
  exit 1
fi
docker build -t "${DOCKER_USER}/json2hat" .
docker build -f Dockerfile.debug -t "${DOCKER_USER}/json2hat-debug" .
