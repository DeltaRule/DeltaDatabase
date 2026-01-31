package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"delta-db/pkg/fs"
)

func main() {
	// Create a temporary directory to simulate shared filesystem
	tempDir := filepath.Join(os.TempDir(), "deltadatabase_manual_test")
	defer os.RemoveAll(tempDir)

	fmt.Println("=== DeltaDatabase Filesystem Layer Manual Verification ===\n")
	fmt.Printf("Test directory: %s\n\n", tempDir)

	// Create storage
	storage, err := fs.NewStorage(tempDir)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("✓ Storage created successfully")
	fmt.Printf("  - Files directory: %s\n", storage.GetFilesDir())
	fmt.Printf("  - Templates directory: %s\n\n", storage.GetTemplatesDir())

	// Write a test file
	entityID := "Chat_123"
	encryptedData := []byte("This would be encrypted JSON data in production")
	
	metadata := fs.FileMetadata{
		KeyID:     "key-abc-123",
		Algorithm: "AES-GCM",
		IV:        base64.StdEncoding.EncodeToString([]byte("test-nonce-12")),
		Tag:       base64.StdEncoding.EncodeToString([]byte("auth-tag-16bytes")),
		SchemaID:  "chat.v1",
		Version:   1,
		WriterID:  "worker-1",
		Timestamp: time.Now(),
		Database:  "chatdb",
		EntityKey: entityID,
	}

	fmt.Printf("Writing entity '%s'...\n", entityID)
	err = storage.WriteFile(entityID, encryptedData, metadata)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("✓ Entity written successfully\n")

	// Verify files exist
	blobPath := storage.GetBlobPath(entityID)
	metaPath := storage.GetMetaPath(entityID)

	fmt.Println("File layout verification:")
	fmt.Printf("  - Encrypted blob: %s\n", blobPath)
	fmt.Printf("    Exists: %v\n", fileExists(blobPath))
	
	fmt.Printf("  - Metadata: %s\n", metaPath)
	fmt.Printf("    Exists: %v\n\n", fileExists(metaPath))

	// Read the file back
	fmt.Println("Reading entity back...")
	fileData, err := storage.ReadFile(entityID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("✓ Entity read successfully")
	fmt.Printf("  - Data size: %d bytes\n", len(fileData.Blob))
	fmt.Printf("  - Schema ID: %s\n", fileData.Metadata.SchemaID)
	fmt.Printf("  - Version: %d\n", fileData.Metadata.Version)
	fmt.Printf("  - Writer ID: %s\n\n", fileData.Metadata.WriterID)

	// Test file locking
	fmt.Println("Testing file locking...")
	lockManager := fs.NewLockManager(storage)

	// Acquire exclusive lock
	_, err = lockManager.AcquireLock(entityID, fs.LockExclusive)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("✓ Exclusive lock acquired")

	// Lock file should exist
	lockPath := blobPath + ".lock"
	fmt.Printf("  - Lock file: %s\n", lockPath)
	fmt.Printf("    Exists: %v\n", fileExists(lockPath))

	// Release lock
	err = lockManager.ReleaseLock(entityID)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("✓ Lock released\n")

	// Write a schema template
	fmt.Println("Writing schema template...")
	schemaData := []byte(`{
  "$id": "chat.v1",
  "type": "object",
  "properties": {
    "chat": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "type": {"type": "string"},
          "text": {"type": "string"}
        },
        "required": ["type", "text"]
      }
    }
  },
  "required": ["chat"]
}`)

	err = storage.WriteTemplate("chat.v1", schemaData)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("✓ Schema template written")

	templatePath := filepath.Join(storage.GetTemplatesDir(), "chat.v1.json")
	fmt.Printf("  - Template file: %s\n", templatePath)
	fmt.Printf("    Exists: %v\n\n", fileExists(templatePath))

	// List all files
	fmt.Println("Listing all entities...")
	files, err := storage.ListFiles()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("✓ Found %d entity/entities:\n", len(files))
	for _, file := range files {
		fmt.Printf("  - %s\n", file)
	}
	fmt.Println()

	// Show directory tree
	fmt.Println("Final directory structure:")
	printDirTree(tempDir, "")

	fmt.Println("\n=== Manual Verification Complete ===")
	fmt.Println("All filesystem operations working correctly!")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func printDirTree(path string, prefix string) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}

	for i, entry := range entries {
		isLast := i == len(entries)-1
		connector := "├── "
		if isLast {
			connector = "└── "
		}

		fmt.Printf("%s%s%s\n", prefix, connector, entry.Name())

		if entry.IsDir() {
			newPrefix := prefix
			if isLast {
				newPrefix += "    "
			} else {
				newPrefix += "│   "
			}
			printDirTree(filepath.Join(path, entry.Name()), newPrefix)
		}
	}
}
