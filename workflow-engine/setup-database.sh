#!/bin/bash

# Database setup sc# Configuration
DB_HOST=${DB_HOST:-localhost}
DB_PORT=${DB_PORT:-5432}
DB_USER=${DB_USER:-postgres}
DB_PASSWORD=${DB_PASSWORD:-sam}
DB_NAME=${DB_NAME:-workflow_engine}

# Prompt for password if not set and not in non-interactive mode
prompt_password() {
    if [ -z "$DB_PASSWORD" ] || [ "$DB_PASSWORD" = "password" ]; then
        if [ -t 0 ]; then  # Check if running interactively
            echo ""
            print_status "PostgreSQL password not provided or using default."
            read -s -p "Enter PostgreSQL password for user '$DB_USER' (or press Enter for 'password'): " input_password
            echo ""
            if [ ! -z "$input_password" ]; then
                DB_PASSWORD="$input_password"
            fi
        fi
    fi
}

set -e

echo "🗄️  Setting up PostgreSQL database for Workflow Engine"
echo "===================================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Configuration
DB_HOST=${DB_HOST:-localhost}
DB_PORT=${DB_PORT:-5432}
DB_USER=${DB_USER:-postgres}
DB_PASSWORD=${DB_PASSWORD:-sam}
DB_NAME=${DB_NAME:-workflow_engine}

# Check if PostgreSQL is running
check_postgres() {
    print_status "Checking PostgreSQL connection..."
    
    if ! command -v psql &> /dev/null; then
        print_error "PostgreSQL client (psql) is not installed"
        exit 1
    fi
    
    # Set PGPASSWORD environment variable for password authentication
    export PGPASSWORD="$DB_PASSWORD"
    
    if ! pg_isready -h $DB_HOST -p $DB_PORT -U $DB_USER &> /dev/null; then
        print_error "PostgreSQL is not running or not accessible at $DB_HOST:$DB_PORT"
        print_status "Please start PostgreSQL and ensure it's accessible"
        print_status "Also verify the credentials: user=$DB_USER, password=$DB_PASSWORD"
        exit 1
    fi
    
    print_success "PostgreSQL is running and accessible"
}

# Create database if it doesn't exist
create_database() {
    print_status "Creating database '$DB_NAME' if it doesn't exist..."
    
    # Check if database exists
    if PGPASSWORD="$DB_PASSWORD" psql -h $DB_HOST -p $DB_PORT -U $DB_USER -lqt | cut -d \| -f 1 | grep -qw $DB_NAME; then
        print_warning "Database '$DB_NAME' already exists"
    else
        PGPASSWORD="$DB_PASSWORD" createdb -h $DB_HOST -p $DB_PORT -U $DB_USER $DB_NAME
        print_success "Database '$DB_NAME' created successfully"
    fi
}

# Run database initialization script
init_schema() {
    print_status "Initializing database schema..."
    
    if [ -f "go/scripts/init.sql" ]; then
        PGPASSWORD="$DB_PASSWORD" psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -f go/scripts/init.sql
        print_success "Database schema initialized successfully"
    else
        print_error "Database initialization script not found at go/scripts/init.sql"
        exit 1
    fi
}

# Verify schema
verify_schema() {
    print_status "Verifying database schema..."
    
    # Check if tables exist
    tables=$(PGPASSWORD="$DB_PASSWORD" psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -t -c "SELECT table_name FROM information_schema.tables WHERE table_schema = 'waves';")
    
    expected_tables=("endpoint" "workflow" "state" "variables")
    for table in "${expected_tables[@]}"; do
        if echo "$tables" | grep -q "$table"; then
            print_success "✓ Table 'waves.$table' exists"
        else
            print_error "✗ Table 'waves.$table' is missing"
            exit 1
        fi
    done
    
    print_success "All required tables are present"
}

# Main execution
main() {
    echo ""
    prompt_password
    
    print_status "Database Configuration:"
    echo "  Host: $DB_HOST"
    echo "  Port: $DB_PORT"
    echo "  User: $DB_USER"
    echo "  Password: ${DB_PASSWORD:0:1}***"
    echo "  Database: $DB_NAME"
    echo ""
    
    check_postgres
    create_database
    init_schema
    verify_schema
    
    echo ""
    print_success "🎉 Database setup completed successfully!"
    echo ""
    print_status "You can now start the workflow engine system with:"
    echo "  ./test-system.sh"
    echo ""
    print_status "Or start components individually:"
    echo "  cd go && ./bin/workflow-engine"
    echo "  cd worker && ./bin/workflow-worker"
}

# Show help
show_help() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -h, --help     Show this help message"
    echo ""
    echo "Environment Variables:"
    echo "  DB_HOST        PostgreSQL host (default: localhost)"
    echo "  DB_PORT        PostgreSQL port (default: 5432)"
    echo "  DB_USER        PostgreSQL user (default: postgres)"
    echo "  DB_PASSWORD    PostgreSQL password (default: password)"
    echo "  DB_NAME        Database name (default: workflow_engine)"
    echo ""
    echo "Examples:"
    echo "  $0                                              # Use default settings"
    echo "  DB_HOST=myhost DB_USER=myuser DB_PASSWORD=secret $0  # Custom settings"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_help
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
done

# Run main function
main