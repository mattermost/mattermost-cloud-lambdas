#!/bin/bash

# Script to check linting and formatting on all lambda functions
# Stops at the first error to allow fixing

set -e  # Exit on any error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}🔍 Starting check-style on all Lambda functions...${NC}"
echo ""

# Get the script directory (where this script is located)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Array of all lambda directories (based on what we've seen in the repo)
LAMBDA_DIRS=(
    "account-alerts"
    "alert-elb-cloudwatch-alarm"
    "bind-server-network-attachment"
    "cloud-server-auth"
    "cloudwatch-event-alerts"
    "create-elb-cloudwatch-alarm"
    "create-rds-cloudwatch-alarm"
    "deckhand"
    "ebs-janitor"
    "elb-cleanup"
    "elrond-notification"
    "gitlab-webhook"
    "grafana-aws-metrics"
    "grant-privileges-to-schemas"
    "lambda-promtail"
    "provisioner-notification"
    "rds-cluster-events"
)

# Counter for tracking progress
TOTAL=${#LAMBDA_DIRS[@]}
CURRENT=0

# Function to run check-style in a directory
check_lambda() {
    local dir=$1
    CURRENT=$((CURRENT + 1))
    
    echo -e "${BLUE}[$CURRENT/$TOTAL]${NC} Checking ${YELLOW}$dir${NC}..."
    
    # Check if directory exists
    if [ ! -d "$SCRIPT_DIR/$dir" ]; then
        echo -e "${RED}❌ Directory $dir not found!${NC}"
        return 1
    fi
    
    # Check if Makefile exists
    if [ ! -f "$SCRIPT_DIR/$dir/Makefile" ]; then
        echo -e "${RED}❌ No Makefile found in $dir!${NC}"
        return 1
    fi
    
    # Change to the lambda directory and run check-style
    cd "$SCRIPT_DIR/$dir"
    
    echo "   Running: make check-style"
    if make check-style; then
        echo -e "${GREEN}✅ $dir passed!${NC}"
        echo ""
        return 0
    else
        echo -e "${RED}❌ $dir failed check-style!${NC}"
        echo -e "${RED}💡 Please fix the errors above in directory: $dir${NC}"
        echo -e "${YELLOW}📍 Current directory: $(pwd)${NC}"
        echo ""
        echo -e "${BLUE}To continue after fixing:${NC}"
        echo "   cd '$SCRIPT_DIR'"
        echo "   ./check-all-lambdas.sh"
        echo ""
        return 1
    fi
}

# Main loop
echo -e "${BLUE}📋 Found $TOTAL lambda directories to check${NC}"
echo ""

for dir in "${LAMBDA_DIRS[@]}"; do
    if ! check_lambda "$dir"; then
        exit 1
    fi
done

echo -e "${GREEN}🎉 All Lambda functions passed check-style!${NC}"
echo -e "${GREEN}✨ No linting or formatting errors found.${NC}" 