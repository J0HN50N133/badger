# Experiment Folder Rules

## Parameter Input Contract
- All experiment runtime parameters MUST be loaded from a JSON config file.
- The command line MUST accept a JSON file path (for example: `./experiment_binary /path/to/config.json`).
- Experiments MUST NOT hardcode business parameters inside source code.
- If a required parameter is missing from the JSON file, the experiment MUST fail fast with a clear error message.

## Goal
- Keep experiment code reproducible and script-friendly.
