#!/bin/bash
INPUT=$(cat)
TMP_FILE=$(echo "$INPUT" | jq -r '.tmp_file')
OUTPUT_DIR=$(echo "$INPUT" | jq -r '.config.output.dir')
OUTPUT_FILE=$(echo "$INPUT" | jq -r '.config.output.file')
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id')
TURN=$(echo "$INPUT" | jq -r '.turn')
ENRICHED=$(jq -n --arg session "$SESSION_ID" --argjson turn "$TURN" --slurpfile data "$TMP_FILE" \
  '{session: $session, turn: $turn, timestamp: now | todate, errors: $data[0].errors}')
echo "$ENRICHED" >> "$OUTPUT_DIR/$OUTPUT_FILE"
