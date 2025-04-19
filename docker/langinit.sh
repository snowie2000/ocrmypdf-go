#!/bin/bash

# Script to check for an "installed" marker file and install language packs if missing

FILE_MARKER="installed"

# Check if the file exists
if [[ -f "$FILE_MARKER" ]]; then
    # If the file exists, print a message and exit
    echo "Marker file '$FILE_MARKER' found."
    echo "Installation process is considered complete. Exiting."
    exit 0 # Exit successfully, indicating the required state is met
else
    # If the file does NOT exist, proceed with installation steps
    echo "Marker file '$FILE_MARKER' not found."
    echo "Proceeding with installation steps..."

    # --- Installation Logic ---

    # Check if the LANGUAGES environment variable is set and not empty
    if [ -z "$LANGUAGES" ]; then
        echo "Error: LANGUAGES environment variable is not set or is empty."
        echo "Cannot determine which language packs to install."
        # Do NOT create the marker file, as installation didn't happen.
        exit 1 # Exit with an error code to indicate failure
    fi

    echo "LANGUAGES environment variable found: '$LANGUAGES'"

    # Split the comma-separated LANGUAGES string into an array
    # Use IFS (Internal Field Separator) temporarily set to comma
    IFS=',' read -ra language_codes <<< "$LANGUAGES"

    # Build the list of tesseract package names
    package_list=()
    for lang in "${language_codes[@]}"; do
        # Trim potential whitespace and check if the resulting language code is non-empty
        # Use xargs to handle leading/trailing whitespace
        lang=$(echo "$lang" | xargs)
        if [ -n "$lang" ]; then
            package_list+=("tesseract-ocr-${lang}")
        fi
    done

    # Check if any packages were successfully added to the list
    if [ ${#package_list[@]} -eq 0 ]; then
        echo "Error: No valid language codes found after processing '$LANGUAGES'."
        echo "No packages to install."
        # Do NOT create the marker file
        exit 1 # Exit with an error code
    fi

    echo "Identified packages to install: ${package_list[*]}" # ${package_list[*]} is good for display

    # Run apt update
    echo "Running apt update..."
    # Use `if ! command; then ...` to check for non-zero exit status (failure)
    if ! apt update; then
        echo "Error during apt update."
        # Do NOT create the marker file
        exit 1 # Exit with an error code
    fi

    # Run apt install
    echo "Running apt install -y ${package_list[*]}..."
    # Use "${package_list[@]}" to pass each element as a separate argument
    if ! apt install -y "${package_list[@]}"; then
        echo "Error during apt install."
        # Do NOT create the marker file
        exit 1 # Exit with an error code
    fi

    echo "Successfully installed language packs."

    # --- Installation Logic Ends ---

    # Create the marker file ONLY if the installation commands were successful
    echo "Creating marker file '$FILE_MARKER' to prevent future runs..."
    touch "$FILE_MARKER"

    echo "Installation script finished successfully."
    exit 0 # Exit successfully
fi

# This line is typically not reached due to the exit commands within the if/else blocks.
