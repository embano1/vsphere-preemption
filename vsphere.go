package preemption

import (
	"context"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/session/keepalive"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/soap"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/logging"
)

const (
	DefaultMountPath  = "/var/bindings/vsphere" // vsphere secret
	keepaliveInterval = 5 * time.Minute         // vCenter APIs keep-alive
)

// ReadKey reads the key from the secret.
func ReadKey(key string) (string, error) {
	var env EnvConfig
	if err := envconfig.Process("", &env); err != nil {
		return "", err
	}

	mountPath := DefaultMountPath
	if env.SecretPath != "" {
		mountPath = env.SecretPath
	}

	data, err := ioutil.ReadFile(filepath.Join(mountPath, key))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// newSOAPClient returns a vCenter SOAP API client with active keep-alive. Use
// Logout() to release resources and perform a clean logout from vCenter.
func newSOAPClient(ctx context.Context) (*govmomi.Client, error) {
	var env EnvConfig
	if err := envconfig.Process("", &env); err != nil {
		return nil, err
	}

	parsedURL, err := soap.ParseURL(env.VCAddress)
	if err != nil {
		return nil, err
	}

	// Read the username and password from the filesystem.
	username, err := ReadKey(corev1.BasicAuthUsernameKey)
	if err != nil {
		return nil, err
	}
	password, err := ReadKey(corev1.BasicAuthPasswordKey)
	if err != nil {
		return nil, err
	}
	parsedURL.User = url.UserPassword(username, password)

	return soapWithKeepalive(ctx, parsedURL, env.Insecure)
}

func soapWithKeepalive(ctx context.Context, url *url.URL, insecure bool) (*govmomi.Client, error) {
	sc := soap.NewClient(url, insecure)
	vc, err := vim25.NewClient(ctx, sc)
	if err != nil {
		return nil, err
	}
	vc.RoundTripper = keepalive.NewHandlerSOAP(sc, keepaliveInterval, soapKeepAliveHandler(ctx, vc))

	// explicitly create session to activate keep-alive handler via Login
	m := session.NewManager(vc)
	err = m.Login(ctx, url.User)
	if err != nil {
		return nil, err
	}

	c := govmomi.Client{
		Client:         vc,
		SessionManager: m,
	}

	return &c, nil
}

func soapKeepAliveHandler(ctx context.Context, c *vim25.Client) func() error {
	logger := logging.FromContext(ctx)

	return func() error {
		logger.Debug("executing SOAP keep-alive handler")
		t, err := methods.GetCurrentTime(ctx, c)
		if err != nil {
			logger.Errorw("execute SOAP keep-alive handler", zap.Error(err))
			return err
		}

		logger.Debug("vCenter current time", "time", t.String())
		return nil
	}
}

// newRESTClient returns a vCenter REST API client with active keep-alive. Use
// Logout() to release resources and perform a clean logout from vCenter.
func newRESTClient(ctx context.Context, vc *vim25.Client) (*rest.Client, error) {
	var env EnvConfig
	if err := envconfig.Process("", &env); err != nil {
		return nil, err
	}

	parsedURL, err := soap.ParseURL(env.VCAddress)
	if err != nil {
		return nil, err
	}

	// Read the username and password from the filesystem.
	username, err := ReadKey(corev1.BasicAuthUsernameKey)
	if err != nil {
		return nil, err
	}
	password, err := ReadKey(corev1.BasicAuthPasswordKey)
	if err != nil {
		return nil, err
	}
	parsedURL.User = url.UserPassword(username, password)

	rc := rest.NewClient(vc)
	rc.Transport = keepalive.NewHandlerREST(rc, keepaliveInterval, restKeepAliveHandler(ctx, rc))

	// Login activates the keep-alive handler
	if err = rc.Login(ctx, parsedURL.User); err != nil {
		return nil, err
	}
	return rc, nil
}

func restKeepAliveHandler(ctx context.Context, restclient *rest.Client) func() error {
	logger := logging.FromContext(ctx)

	return func() error {
		logger.Debug("executing REST keep-alive handler")
		s, err := restclient.Session(ctx)
		if err != nil {
			// errors are not logged in govmomi keepalive handler
			logger.Errorw("execute REST keep-alive handler", zap.Error(err))
			return err
		}
		if s != nil {
			return nil
		}
		logger.Errorw("execute REST keep-alive handler", zap.Error(err))
		return errors.New(http.StatusText(http.StatusUnauthorized))
	}
}
