#!/bin/bash
INPUT=$(cat)
ERRORS=$(echo "$INPUT" | jq '.errors')
PROMPT="Categorize these errors from the current session:\n$ERRORS\n\nReturn JSON: {errors: [{type, context, resolution, frequency}]}"
echo "$INPUT" | jq --arg prompt "$PROMPT" '{
  user_prompt: $prompt,
  output_structure: {errors: [{type: "string", context: "string", resolution: "string", frequency: "number"}]}
}'
