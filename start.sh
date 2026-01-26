#!/bin/sh
echo "--- Cron Job Runner Started ---"
echo "Current Time: $(date)"
echo "Waiting for cron schedule..."
# コンテナを落とさないために待機
tail -f /dev/null