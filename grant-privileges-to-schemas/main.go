// Package main provides a Lambda function to manage PostgreSQL permissions for schemas and databases
// within multi-tenant RDS clusters. It fetches credentials, logical database mappings, and applies
// appropriate permissions for reader and writer roles.
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
	excludedClusters  = parseExcludedClusters(os.Getenv("EXCLUDED_CLUSTERS"))
)

// Secrets manager client
var smClient *secretsmanager.SecretsManager

func init() {
	sess := session.Must(session.NewSession())
	smClient = secretsmanager.New(sess)
}

// parseExcludedClusters parses a comma-separated list of excluded clusters.
func parseExcludedClusters(excluded string) map[string]struct{} {
	clusters := strings.Split(excluded, ",")
	excludedMap := make(map[string]struct{})
	for _, cluster := range clusters {
		trimmed := strings.TrimSpace(cluster)
		if trimmed != "" {
			excludedMap[trimmed] = struct{}{}
		}
	}
	return excludedMap
}

// isExcludedCluster checks if a cluster is in the excluded list.
func isExcludedCluster(cluster string) bool {
	_, exists := excludedClusters[cluster]
	return exists
}

// GetSecret retrieves the secret value from AWS Secrets Manager.
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

// getActivityDate retrieves and parses the activity date.
func getActivityDate() (int64, error) {
	dateStr := os.Getenv("ACTIVITY_DATE")

	switch dateStr {
	case "now":
		dateStr = time.Now().Format("2006-01-02")
	case "":
		dateStr = "2020-09-01"
	}

	parsedDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return 0, fmt.Errorf("invalid date format: %w", err)
	}
	return parsedDate.UTC().UnixMilli(), nil
}

// getWriterEndpoint fetches the writer endpoint for a given RDS cluster.
func getWriterEndpoint(clusterIdentifier string) (string, error) {
	sess := session.Must(session.NewSession())
	rdsClient := rds.New(sess)

	input := &rds.DescribeDBClustersInput{
		DBClusterIdentifier: aws.String(clusterIdentifier),
	}
	output, err := rdsClient.DescribeDBClusters(input)
	if err != nil {
		return "", fmt.Errorf("failed to describe RDS cluster %s: %w", clusterIdentifier, err)
	}

	if len(output.DBClusters) == 0 || output.DBClusters[0].Endpoint == nil {
		return "", fmt.Errorf("writer endpoint not found for cluster %s", clusterIdentifier)
	}

	return *output.DBClusters[0].Endpoint, nil
}

// fetchSchemasAndClusters retrieves schema-to-database and database-to-cluster mappings.
func fetchSchemasAndClusters(provisionerDB *sql.DB, activityDate int64) (map[string]string, map[string]string, error) {
	query := `
		SELECT 
		    ds.name AS schema_name, 
		    ld.id AS logical_database_id, 
		    mt.rdsclusterid AS rds_cluster_id
		FROM 
		    public.databaseschema ds
		JOIN 
		    public.logicaldatabase ld 
		    ON ds.logicaldatabaseid = ld.id
		JOIN 
		    public.multitenantdatabase mt 
		    ON ld.multitenantdatabaseid = mt.id
		WHERE 
		    ds.createat >= $1 
		    AND ds.deleteat = 0 
		    AND ds.name LIKE 'id_%';`

	rows, err := provisionerDB.Query(query, activityDate)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query schemas: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			log.Printf("Failed to close rows: %v", closeErr)
		}
	}()

	schemaToDB := make(map[string]string)  // Map schema name to logical database
	dbToCluster := make(map[string]string) // Map logical database to RDS cluster ID

	for rows.Next() {
		var schemaName, logicalDatabaseID, rdsClusterID string
		if err := rows.Scan(&schemaName, &logicalDatabaseID, &rdsClusterID); err != nil {
			return nil, nil, fmt.Errorf("failed to scan schema row: %w", err)
		}

		logicalDatabase := fmt.Sprintf("cloud_%s", logicalDatabaseID)
		schemaToDB[schemaName] = logicalDatabase
		dbToCluster[logicalDatabase] = rdsClusterID
	}

	return schemaToDB, dbToCluster, nil
}

// applyPermissionsToDatabase applies the necessary permissions to schemas and tables.
func applyPermissionsToDatabase(db *sql.DB, schemas map[string]string, logicalDatabase string, cluster string) error {
	for schema, targetDB := range schemas {
		if targetDB != logicalDatabase {
			continue
		}

		log.Printf("Running privileges on schema %s which lives in %s, in cluster %s", schema, logicalDatabase, cluster)

		// Grant permissions for reader user
		if _, err := db.Exec(fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s;", schema, readerUser)); err != nil {
			log.Printf("Failed to grant USAGE on schema %s to %s: %v", schema, readerUser, err)
		} else {
			log.Printf("Granted USAGE on schema %s to %s", schema, readerUser)
		}

		if _, err := db.Exec(fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA %s TO %s;", schema, readerUser)); err != nil {
			log.Printf("Failed to grant SELECT on all tables in schema %s to %s: %v", schema, readerUser, err)
		} else {
			log.Printf("Granted SELECT on all tables in schema %s to %s", schema, readerUser)
		}

		// Grant permissions for writer user
		if _, err := db.Exec(fmt.Sprintf("GRANT USAGE, CREATE ON SCHEMA %s TO %s;", schema, writerUser)); err != nil {
			log.Printf("Failed to grant USAGE, CREATE on schema %s to %s: %v", schema, writerUser, err)
		} else {
			log.Printf("Granted USAGE, CREATE on schema %s to %s", schema, writerUser)
		}

		if _, err := db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA %s TO %s;", schema, writerUser)); err != nil {
			log.Printf("Failed to grant ALL PRIVILEGES on all tables in schema %s to %s: %v", schema, writerUser, err)
		} else {
			log.Printf("Granted ALL PRIVILEGES on all tables in schema %s to %s", schema, writerUser)
		}
	}

	return nil
}

// Handler is the main entry point for the Lambda function.
func Handler(_ context.Context) error {
	provisionerSecret := fmt.Sprintf("provisioner-%s", environment)
	provisionerPassword, err := GetSecret(provisionerSecret)
	if err != nil {
		return fmt.Errorf("failed to retrieve provisioner DB password: %w", err)
	}

	provisionerConnStr := fmt.Sprintf("host=%s user=%s password=%s dbname=cloud sslmode=disable", provisionerDBURL, provisionerDBUser, provisionerPassword)
	provisionerDB, err := sql.Open("postgres", provisionerConnStr)
	if err != nil {
		return fmt.Errorf("failed to connect to provisioner database: %w", err)
	}
	defer func() {
		if closeErr := provisionerDB.Close(); closeErr != nil {
			log.Printf("Failed to close provisioner database: %v", closeErr)
		}
	}()

	activityDate, err := getActivityDate()
	if err != nil {
		return fmt.Errorf("failed to parse activity date: %w", err)
	}

	schemaToDB, dbToCluster, err := fetchSchemasAndClusters(provisionerDB, activityDate)
	if err != nil {
		return fmt.Errorf("failed to fetch schemas and clusters: %w", err)
	}

	for logicalDatabase, cluster := range dbToCluster {
		if isExcludedCluster(cluster) {
			log.Printf("Skipping excluded cluster %s", cluster)
			continue
		}

		writerEndpoint, err := getWriterEndpoint(cluster)
		if err != nil {
			log.Printf("Failed to retrieve writer endpoint for cluster %s: %v", cluster, err)
			continue
		}

		password, err := GetSecret(cluster)
		if err != nil {
			log.Printf("Failed to retrieve password for cluster %s: %v", cluster, err)
			continue
		}

		connStr := fmt.Sprintf("host=%s user=%s password=%s dbname=%s sslmode=disable", writerEndpoint, dbUsername, password, logicalDatabase)
		db, err := sql.Open("postgres", connStr)
		if err != nil {
			log.Printf("Failed to connect to logical database %s: %v", logicalDatabase, err)
			continue
		}
		defer func() {
			if closeErr := db.Close(); closeErr != nil {
				log.Printf("Failed to close database connection for %s: %v", logicalDatabase, closeErr)
			}
		}()

		if err := applyPermissionsToDatabase(db, schemaToDB, logicalDatabase, cluster); err != nil {
			log.Printf("Failed to apply permissions to database %s: %v", logicalDatabase, err)
		}
	}

	log.Println("Permissions successfully applied across all databases and clusters.")
	return nil
}

func main() {
	lambda.Start(Handler)
}
