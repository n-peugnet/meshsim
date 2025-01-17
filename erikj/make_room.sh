#!/bin/bash

ID=$1

port=$(expr 18000 + $i + $ID \* 100)
curl -XPOST "http://localhost:$port/_matrix/client/r0/createRoom?access_token=fake_token" -d '{"preset":"public_chat", "room_alias_name": "test"}'
