#!/bin/sh
# Counts which weighted TCP backend answers each connection.
GW_IP=${GW_IP:-localhost}
PORT=${PORT:-31447}

COUNTER=0
while [ $COUNTER -lt 100 ]; do
  if curl -sk --resolve "tcp-weighted.example.local:$PORT:$GW_IP" \
      "https://tcp-weighted.example.local:$PORT/hostname" | grep -q '^tcp-weighted-blue'; then
    echo blue
  else
    echo green
  fi
  COUNTER=$((COUNTER + 1))
done | sort | uniq -c
