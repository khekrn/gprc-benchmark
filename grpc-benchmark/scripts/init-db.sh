#!/bin/bash

# Database initialization script for gRPC benchmark
set -e

# Load environment variables
source .env

# Database connection details
DB_HOST="localhost"
DB_PORT="5432"
DB_NAME="proddb"
DB_USER="postgres"
DB_SCHEMA="benchmark"

echo "Initializing database schema for gRPC benchmark..."

# Check if PostgreSQL is running
if ! pg_isready -h $DB_HOST -p $DB_PORT -U $DB_USER; then
    echo "Error: PostgreSQL is not running on $DB_HOST:$DB_PORT"
    echo "Please start PostgreSQL and ensure the 'proddb' database exists."
    exit 1
fi

# Check if database exists
if ! psql -h $DB_HOST -p $DB_PORT -U $DB_USER -lqt | cut -d \| -f 1 | grep -qw $DB_NAME; then
    echo "Error: Database '$DB_NAME' does not exist."
    echo "Please create the database first:"
    echo "  createdb -h $DB_HOST -p $DB_PORT -U $DB_USER $DB_NAME"
    exit 1
fi

# Initialize schema
echo "Creating benchmark schema and tables..."
psql -h $DB_HOST -p $DB_PORT -U $DB_USER -d $DB_NAME -f internal/db/schema.sql

echo "Database schema initialized successfully!"
echo "Schema '$DB_SCHEMA' created in database '$DB_NAME'"
