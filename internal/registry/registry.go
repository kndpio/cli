package registry

import (
	"context"
	b64 "encoding/base64"
	"encoding/json"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/go-playground/validator/v10"
	"github.com/kndpio/kndp/internal/engine"
	"github.com/kndpio/kndp/internal/kube"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

const RegistryServerLabel = "kndp-registry-server-url"

type RegistryAuth struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
	Email    string `json:"email" validate:"required,email"`
	Auth     string `json:"auth"`
}

type RegistryConfig struct {
	Auths map[string]RegistryAuth `json:"auths"`
}

type Registry struct {
	Config RegistryConfig
	corev1.Secret
}

// Return regestires from requested context
func Registries(ctx context.Context, client *kubernetes.Clientset) ([]*Registry, error) {
	secrets, err := secretClient(client).
		List(ctx, metav1.ListOptions{LabelSelector: "kndp-registry-auth-config=true"})
	if err != nil {
		return nil, err
	}
	registries := []*Registry{}
	for _, secret := range secrets.Items {
		registry := Registry{}
		registries = append(registries, registry.FromSecret(secret))
	}
	return registries, nil
}

// Creates new Registry by required parameters
func New(server string, username string, password string, email string) Registry {
	registry := Registry{
		Config: RegistryConfig{
			Auths: map[string]RegistryAuth{
				server: {
					Username: username,
					Password: password,
					Email:    email,
					Auth:     b64.StdEncoding.EncodeToString([]byte(username + ":" + password)),
				},
			},
		},
	}
	return registry
}

// Validate data in Registry object
func (r *Registry) Validate() error {
	validate := validator.New(validator.WithRequiredStructEnabled())
	for serverUrl, auth := range r.Config.Auths {

		err := validate.Var(serverUrl, "required,http_url")
		if err != nil {
			return err
		}

		err = validate.Struct(auth)
		if err != nil {
			return err
		}
	}
	return nil
}

// Check if registry in provided context exists
func (r *Registry) Exists(ctx context.Context, client *kubernetes.Clientset) bool {
	registries, _ := Registries(ctx, client)
	for _, registry := range registries {
		for authServer := range r.Config.Auths {
			if existsUrl := registry.Annotations[RegistryServerLabel]; existsUrl != "" && strings.Contains(existsUrl, authServer) {
				return true
			}
		}
		if existsUrl := registry.Annotations[RegistryServerLabel]; existsUrl != "" && strings.Contains(existsUrl, r.Annotations[RegistryServerLabel]) {
			return true
		}
	}
	return false
}

// Creates specs of Secret base on Registry data
func (r *Registry) SecretSpec() corev1.Secret {
	regConf, _ := json.Marshal(r.Config)
	servers := []string{}
	for authServer := range r.Config.Auths {
		servers = append(servers, authServer)
	}

	secretSpec := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "registry-server-auth-",
			Labels: engine.ManagedLabels(map[string]string{
				"kndp-registry-auth-config": "true",
			}),
			Annotations: map[string]string{
				RegistryServerLabel: strings.Join(servers, ","),
			},
		},
		Data: map[string][]byte{".dockerconfigjson": regConf},
		Type: "kubernetes.io/dockerconfigjson",
	}

	return secretSpec
}

// Creates registry in requested context and assign it to engine
func (r *Registry) Create(ctx context.Context, config *rest.Config, logger *log.Logger) error {

	client, err := kube.Client(config)
	if err != nil {
		return err
	}

	secretSpec := r.SecretSpec()
	secret, err := secretClient(client).Create(ctx, &secretSpec, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	installer, err := engine.GetEngine(config)
	if err != nil {
		return err
	}

	release, _ := installer.GetRelease()

	if release.Config == nil {
		release.Config = map[string]interface{}{
			"imagePullSecrets": []interface{}{},
		}
	}
	if release.Config["imagePullSecrets"] == nil {
		release.Config["imagePullSecrets"] = []interface{}{}
	}
	release.Config["imagePullSecrets"] = append(
		release.Config["imagePullSecrets"].([]interface{}),
		secret.ObjectMeta.Name,
	)

	logger.Debug("Upgrade Corssplane chart", "Values", release.Config)

	return installer.Upgrade("", release.Config)
}

func (r *Registry) FromSecret(sec corev1.Secret) *Registry {
	secJson, _ := json.Marshal(sec)
	json.Unmarshal(secJson, r)
	return r
}

func (r *Registry) ToSecret() *corev1.Secret {
	sec := corev1.Secret{}
	rJson, _ := json.Marshal(r)
	json.Unmarshal(rJson, &sec)
	return &sec
}
func (r *Registry) Delete(ctx context.Context, client *kubernetes.Clientset) error {
	return secretClient(client).Delete(ctx, r.Name, metav1.DeleteOptions{})
}

// Copy registries from source to destination contexts
func CopyRegistries(ctx context.Context, logger *log.Logger, sourceConfig *rest.Config, destinationConfig *rest.Config) error {

	destClient, err := kube.Client(destinationConfig)
	if err != nil {
		return err
	}

	sourceClient, err := kube.Client(sourceConfig)
	if err != nil {
		return err
	}

	registries, err := Registries(ctx, sourceClient)
	if err != nil {
		return err
	}

	if len(registries) > 0 {
		for _, registry := range registries {
			if !registry.Exists(ctx, destClient) {
				registry.SetResourceVersion("")
				_, err = destClient.CoreV1().Secrets(engine.Namespace).Create(ctx, registry.ToSecret(), metav1.CreateOptions{})
				if err != nil {
					return err
				}
			} else {
				logger.Warn("Registry for " + registry.Annotations[RegistryServerLabel] + "already exist inside of destination context.")
			}

		}
		logger.Info("Registries copied successfully.")
	} else {
		logger.Warn("Registries not found")
	}
	return nil
}

func secretClient(client *kubernetes.Clientset) kv1.SecretInterface {
	return client.CoreV1().Secrets(engine.Namespace)
}
