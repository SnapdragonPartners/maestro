// probe starts one multipart upload and prints what minio-go parses from
// the ListMultipartUploads answer, so a candidate's Initiated field can be
// read the way the adapter will read it.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func main() {
	endpoint, bucket := os.Args[1], os.Args[2]
	creds := credentials.NewStaticV4(os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), "")
	core, err := minio.NewCore(endpoint, &minio.Options{Creds: creds})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	if ok, _ := core.BucketExists(ctx, bucket); !ok {
		if err := core.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			panic(err)
		}
	}
	id, err := core.NewMultipartUpload(ctx, bucket, "probe/key", minio.PutObjectOptions{})
	if err != nil {
		panic(err)
	}
	if _, err := core.PutObjectPart(ctx, bucket, "probe/key", id, 1, strings.NewReader("hello"), 5, minio.PutObjectPartOptions{}); err != nil {
		panic(err)
	}
	// What the SDK parsed. The raw XML was read separately with
	// `aws --debug s3api list-multipart-uploads`, which is how the missing
	// <Initiated> element was confirmed at the wire.
	result, err := core.ListMultipartUploads(ctx, bucket, "", "", "", "", 1000)
	if err != nil {
		panic(err)
	}
	for _, up := range result.Uploads {
		fmt.Printf("parsed: key=%s id=%s initiated=%v\n", up.Key, up.UploadID, up.Initiated)
	}
	_ = core.AbortMultipartUpload(ctx, bucket, "probe/key", id)
}
