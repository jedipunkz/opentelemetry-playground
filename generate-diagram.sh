#!/bin/bash

# Generate diagram from Mermaid file
# Requires: npm install -g @mermaid-js/mermaid-cli

if command -v mmdc &> /dev/null; then
    echo "Generating architecture diagram..."
    mmdc -i architecture.mmd -o architecture.png
    echo "Diagram generated: architecture.png"
else
    echo "mermaid-cli not found. Install with: npm install -g @mermaid-js/mermaid-cli"
    echo "Or use online Mermaid editor: https://mermaid.live/"
fi 