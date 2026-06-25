package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

// Uploads a known blob into azurite so the deferred-binding test has data.
const connStr = "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==;BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;"

func main() {
	container := "test-container"
	name := "hello.txt"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	content := []byte("THIS-IS-THE-FULL-BLOB-CONTENT-PAYLOAD")

	client, err := azblob.NewClientFromConnectionString(connStr, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "client:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	_, _ = client.CreateContainer(ctx, container, nil)
	_, err = client.UploadBuffer(ctx, container, name, content, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upload:", err)
		os.Exit(1)
	}
	fmt.Printf("uploaded %s/%s (%d bytes)\n", container, name, len(content))
}
