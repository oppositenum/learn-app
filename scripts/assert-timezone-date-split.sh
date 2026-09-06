#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: TEST_DATABASE_URL=... $0 <process-timezone>" >&2
  exit 2
fi
if [ -z "${TEST_DATABASE_URL:-}" ]; then
  echo "TEST_DATABASE_URL must point to the PostgreSQL integration database" >&2
  exit 2
fi

process_timezone=$1
process_date=$(TZ="$process_timezone" date +%F)
database_date=$(psql "$TEST_DATABASE_URL" -X -qAt -v ON_ERROR_STOP=1 \
  -c "SET TIME ZONE 'Asia/Shanghai'; SELECT current_date::text;")

echo "process timezone/date: $process_timezone $process_date"
echo "PostgreSQL timezone/date: Asia/Shanghai $database_date"

if [ "$process_date" = "$database_date" ]; then
  echo "selected timezone did not produce a calendar-date split; verification is invalid" >&2
  exit 1
fi
