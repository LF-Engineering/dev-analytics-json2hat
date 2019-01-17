#!/bin/bash
docker run --env-file <(env | grep SH_) json2hat json2hat
