package platform

type SecretStore interface {
	Get(service, account string) ([]byte, bool, error)
	Put(service, account string, value []byte) error
	Delete(service, account string) error
}
