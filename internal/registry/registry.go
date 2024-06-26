package registry

import (
	"context"
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/go-playground/validator/v10"
	"github.com/kndpio/kndp/internal/engine"
	"github.com/kndpio/kndp/internal/kube"
	"github.com/kndpio/kndp/internal/namespace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

const (
	RegistryServerLabel = "kndp-registry-server-url"
	DefaultRemoteDomain = "xpkg.upbound.io"
	LocalServiceName    = "registry"
	DefaultLocalDomain  = LocalServiceName + "." + namespace.Namespace + ".svc.cluster.local"
)

type RegistryAuth struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
	Email    string `json:"email" validate:"required,email"`
	Auth     string `json:"auth"`
	Server   string `json:"server" validate:"required,http_url"`
}

type RegistryConfig struct {
	Auths map[string]RegistryAuth `json:"auths"`
}

type Registry struct {
	Config  RegistryConfig
	Default bool
	Local   bool
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
		Default: false,
		Config: RegistryConfig{
			Auths: map[string]RegistryAuth{
				server: {
					Username: username,
					Password: password,
					Email:    email,
					Auth:     b64.StdEncoding.EncodeToString([]byte(username + ":" + password)),
					Server:   server,
				},
			},
		},
	}
	registry.Annotations = map[string]string{
		RegistryServerLabel: server,
	}
	return registry
}

// Validate data in Registry object
func (r *Registry) Validate(ctx context.Context, client *kubernetes.Clientset, logger *log.Logger) error {
	if r.Local {
		return nil
	}
	validate := validator.New(validator.WithRequiredStructEnabled())
	for _, auth := range r.Config.Auths {
		err := validate.Struct(auth)
		if err != nil {
			return fmt.Errorf(err.Error())
		}
	}
	if r.Exists(ctx, client) {
		return fmt.Errorf("secret for this registry server already exists")
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
	secretSpec := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "registry-server-auth-",
			Labels: engine.ManagedLabels(map[string]string{
				"kndp-registry-auth-config": "true",
			}),
			Annotations: r.Annotations,
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
	installer, err := engine.GetEngine(config)
	if err != nil {
		return err
	}

	release, _ := installer.GetRelease()

	if r.Local {
		err := r.CreateLocal(ctx, client)
		if err != nil {
			return err
		}
	} else {
		secretSpec := r.SecretSpec()
		secret, err := secretClient(client).Create(ctx, &secretSpec, metav1.CreateOptions{})
		if err != nil {
			return err
		}

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
	}

	if r.Default {
		if release.Config["args"] == nil {
			release.Config["args"] = []interface{}{}
		}
		args := []string{}
		for _, arg := range release.Config["args"].([]interface{}) {
			if !strings.Contains(arg.(string), "--registry") {
				args = append(args, arg.(string))
			}
		}

		release.Config["args"] = append(
			args,
			"--registry="+r.Domain(),
		)
	}

	logger.Debug("Upgrade Corssplane chart", "Values", release.Config)

	return installer.Upgrade(engine.Version, release.Config)
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

// Delete registry
func (r *Registry) Delete(ctx context.Context, config *rest.Config, logger *log.Logger) error {

	installer, err := engine.GetEngine(config)
	if err != nil {
		logger.Errorf(" %v\n", err)
	}

	release, _ := installer.GetRelease()

	if release.Config == nil || release.Config["imagePullSecrets"] == nil {
		logger.Warn("Not found any registry in context.")
	} else {
		oldRegistries := release.Config["imagePullSecrets"].([]interface{})

		newRegistries := []interface{}{}
		for _, reg := range oldRegistries {
			if reg != r.Name {
				newRegistries = append(
					newRegistries,
					reg,
				)
			}
		}
		release.Config["imagePullSecrets"] = newRegistries
		if len(oldRegistries) == len(newRegistries) {
			logger.Warn("Configuration URL not found applied configurations.")
			return nil
		}

		if r.Default {
			if release.Config["args"] != nil {
				args := []string{}
				for _, arg := range release.Config["args"].([]interface{}) {
					if !strings.Contains(arg.(string), "--registry") {
						args = append(args, arg.(string))
					}
				}

				release.Config["args"] = args
			}
		}

		err = installer.Upgrade(engine.Version, release.Config)
		if err != nil {
			return err
		}
	}

	client, err := kube.Client(config)
	if err != nil {
		return err
	}

	if r.Local {
		r.DeleteLocal(ctx, client, logger)
	}

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
				_, err = destClient.CoreV1().Secrets(namespace.Namespace).Create(ctx, registry.ToSecret(), metav1.CreateOptions{})
				if err != nil {
					return err
				}
			} else {
				logger.Warn("Registry for " + registry.Annotations[RegistryServerLabel] + " already exist inside of destination context.")
			}

		}
		logger.Info("Registries copied successfully.")
	} else {
		logger.Warn("Registries not found")
	}
	return nil
}

// Make registry default
func (r *Registry) SetDefault(d bool) {
	r.Default = d
}

// Make local registry
func (r *Registry) SetLocal(l bool) {
	r.Local = l
}

// Domain of primary registry
func (r *Registry) Domain() string {
	if r.Local {
		return DefaultLocalDomain
	}
	domain := DefaultRemoteDomain
	for server := range r.Config.Auths {
		domain = strings.Split(server, "/")[2]
		break
	}
	return domain
}

func secretClient(client *kubernetes.Clientset) kv1.SecretInterface {
	return client.CoreV1().Secrets(namespace.Namespace)
}
