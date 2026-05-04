package firestore

import (
	"context"
	"strings"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type kvDoc struct {
	Key  string `firestore:"key"`
	Data []byte `firestore:"data"`
}

// FirestoreKeyValueStore implements store.KeyValueStore backed by a Firestore collection.
type FirestoreKeyValueStore struct {
	client     *firestore.Client
	collection string
}

// NewFirestoreKeyValueStore creates a KeyValueStore backed by a Firestore collection.
// The collection name is "{prefix}-{namespace}".
func NewFirestoreKeyValueStore(client *firestore.Client, prefix, namespace string) *FirestoreKeyValueStore {
	return &FirestoreKeyValueStore{
		client:     client,
		collection: prefix + "-" + namespace,
	}
}

func (s *FirestoreKeyValueStore) Get(ctx context.Context, key string) ([]byte, error) {
	doc, err := s.client.Collection(s.collection).Doc(sanitizeDocID(key)).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var kv kvDoc
	if err := doc.DataTo(&kv); err != nil {
		return nil, err
	}
	return kv.Data, nil
}

func (s *FirestoreKeyValueStore) Set(ctx context.Context, key string, value []byte) error {
	_, err := s.client.Collection(s.collection).Doc(sanitizeDocID(key)).Set(ctx, kvDoc{
		Key:  key,
		Data: value,
	})
	return err
}

func (s *FirestoreKeyValueStore) Delete(ctx context.Context, key string) error {
	_, err := s.client.Collection(s.collection).Doc(sanitizeDocID(key)).Delete(ctx)
	return err
}

func (s *FirestoreKeyValueStore) List(ctx context.Context, prefix string) ([]string, error) {
	q := s.client.Collection(s.collection).
		Where("key", ">=", prefix).
		Where("key", "<", prefix+"\uffff").
		OrderBy("key", firestore.Asc)

	iter := q.Documents(ctx)
	defer iter.Stop()

	var keys []string
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var kv kvDoc
		if err := doc.DataTo(&kv); err != nil {
			continue
		}
		keys = append(keys, kv.Key)
	}
	return keys, nil
}

func sanitizeDocID(key string) string {
	return strings.ReplaceAll(key, "/", "__")
}
