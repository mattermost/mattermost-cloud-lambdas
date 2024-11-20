// Package main provides a Lambda function to manage PostgreSQL permissions
// based on schema creation dates and a set of configurable parameters.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/rds"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
	_ "github.com/lib/pq"
)

const (
	readerUser = "teleport_db_reader"
	writerUser = "teleport_db_writer"
)

// Environment variables
var (
	dbUsername        = os.Getenv("DB_USERNAME")
	environment       = os.Getenv("ENVIRONMENT")
	provisionerDBURL  = os.Getenv("PROVISIONER_DB_URL")
	provisionerDBUser = os.Getenv("PROVISIONER_DB_USER")
	excludedClusters  = strings.Split(os.Getenv("EXCLUDED_CLUSTERS"), ",") // Comma-separated list
)

// Secrets manager client
var smClient *secretsmanager.SecretsManager

func init() {
	sess := session.Must(session.NewSession())
	smClient = secretsmanager.New(sess)
}

// GetSecret retrieves the secret value from AWS Secrets Manager
func GetSecret(secretName string) (string, error) {
	input := &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretName),
	}
	result, err := smClient.GetSecretValue(input)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve secret %s: %w", secretName, err)
	}
	return *result.SecretString, nil
}

// Check if a cluster should be excluded
func isExcludedCluster(clusterName string) bool {
	for _, excluded := range excludedClusters {
		if strings.TrimSpace(clusterName) == excluded {
			return true
		}
	}
	return false
}

// Fetch all RDS clusters from the AWS account with the specified prefix
func fetchRDSClusters(prefix string) ([]string, error) {
	sess := session.Must(session.NewSession())
	rdsClient := rds.New(sess)

	input := &rds.DescribeDBClustersInput{}
	output, err := rdsClient.DescribeDBClusters(input)
	if err != nil {
		return nil, fmt.Errorf("failed to describe RDS clusters: %w", err)
	}

	var matchingClusters []string
	for _, cluster := range output.DBClusters {
		if cluster.DBClusterIdentifier != nil && strings.HasPrefix(*cluster.DBClusterIdentifier, prefix) {
			if isExcludedCluster(*cluster.DBClusterIdentifier) {
				continue
			}
			matchingClusters = append(matchingClusters, *cluster.DBClusterIdentifier)
		}
	}

	return matchingClusters, nil
}

// Handler function for Lambda
func Handler(_ context.Context) error {
	// Retrieve provisioner DB password
	provisionerSecretName := fmt.Sprintf("provisioner-%s", environment)
	provisionerDBPassword, err := GetSecret(provisionerSecretName)
	if err != nil {
		return fmt.Errorf("failed to get provisioner DB password: %w", err)
	}

	// Connect to provisioner database
	provisionerConnStr := fmt.Sprintf("host=%s user=%s password=%s dbname=cloud sslmode=disable",
		provisionerDBURL, provisionerDBUser, provisionerDBPassword)
	provisionerDB, err := sql.Open("postgres", provisionerConnStr)
	if err != nil {
		return fmt.Errorf("failed to connect to provisioner database: %w", err)
	}
	defer provisionerDB.Close()

	// Get the activity date in UTC milliseconds
	activityDate, err := getActivityDate()
	if err != nil {
		return fmt.Errorf("failed to parse activity date: %w", err)
	}

	// Fetch all clusters with the "rds-cluster-multitenant" prefix
	clusters, err := fetchRDSClusters("rds-cluster-multitenant")
	if err != nil {
		return fmt.Errorf("failed to fetch clusters: %w", err)
	}

	// Iterate through clusters and apply permissions
	for _, cluster := range clusters {
		clusterDBPassword, err := GetSecret(cluster)
		if err != nil {
			log.Printf("Failed to retrieve DB password for cluster %s: %v", cluster, err)
			continue
		}

		if err := applyPermissions(cluster, dbUsername, clusterDBPassword, provisionerDB, activityDate); err != nil {
			log.Printf("Failed to apply permissions for cluster %s: %v", cluster, err)
		}
	}

	log.Println("Permissions successfully granted across all applicable clusters.")
	return nil
}

// Get the activity date as UTC milliseconds from the environment
func getActivityDate() (int64, error) {
	dateStr := os.Getenv("ACTIVITY_DATE")

	if dateStr == "now" {
		dateStr = time.Now().Format("2006-01-02")
	} else if dateStr == "" {
		dateStr = "2020-09-01"
	}

	parsedDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return 0, fmt.Errorf("invalid date format: %w", err)
	}
	return parsedDate.UTC().UnixMilli(), nil
}

// Apply permissions on the target cluster
func applyPermissions(cluster, username, password string, provisionerDB *sql.DB, activityDate int64) error {
	connStr := fmt.Sprintf("host=%s user=%s password=%s dbname=postgres sslmode=disable", cluster, username, password)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to cluster %s: %w", cluster, err)
	}
	defer db.Close()

	schemas, err := fetchEligibleSchemas(provisionerDB, activityDate)
	if err != nil {
		return fmt.Errorf("failed to fetch eligible schemas for cluster %s: %w", cluster, err)
	}

	for _, schema := range schemas {
		if err := grantSchemaPermissions(db, schema, readerUser, writerUser); err != nil {
			log.Printf("Failed to apply permissions on schema %s in cluster %s: %v", schema, cluster, err)
		}
	}

	log.Printf("Permissions applied for cluster %s", cluster)
	return nil
}

// Fetch eligible schemas based on activityDate from the provisionerDB
func fetchEligibleSchemas(provisionerDB *sql.DB, activityDate int64) ([]string, error) {
	query := `SELECT name FROM public.databaseschema WHERE createat >= $1 AND deleteat = 0 AND name LIKE 'id_%';`
	rows, err := provisionerDB.Query(query, activityDate)
	if err != nil {
		return nil, fmt.Errorf("failed to query schemas: %w", err)
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var schema string
		if scanErr := rows.Scan(&schema); scanErr != nil {
			return nil, fmt.Errorf("failed to scan schema name: %w", scanErr)
		}
		schemas = append(schemas, schema)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return schemas, nil
}

// Grant permissions to reader and writer users on the schema and tables
func grantSchemaPermissions(db *sql.DB, schema, readerUser, writerUser string) error {
	if _, err := db.Exec(fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s;", schema, readerUser)); err != nil {
		return fmt.Errorf("failed to grant USAGE on schema %s to %s: %w", schema, readerUser, err)
	}
	if _, err := db.Exec(fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA %s TO %s;", schema, readerUser)); err != nil {
		return fmt.Errorf("failed to grant SELECT on schema %s tables to %s: %w", schema, readerUser, err)
	}

	if _, err := db.Exec(fmt.Sprintf("GRANT USAGE, CREATE ON SCHEMA %s TO %s;", schema, writerUser)); err != nil {
		return fmt.Errorf("failed to grant USAGE, CREATE on schema %s to %s: %w", schema, writerUser, err)
	}
	if _, err := db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA %s TO %s;", schema, writerUser)); err != nil {
		return fmt.Errorf("failed to grant ALL PRIVILEGES on schema %s tables to %s: %w", schema, writerUser, err)
	}
	if _, err := db.Exec(fmt.Sprintf("GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA %s TO %s;", schema, writerUser)); err != nil {
		return fmt.Errorf("failed to grant EXECUTE on schema %s functions to %s: %w", schema, writerUser, err)
	}

	log.Printf("Permissions granted on schema %s to %s and %s", schema, readerUser, writerUser)
	return nil
}

func main() {
	lambda.Start(Handler)
}
