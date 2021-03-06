package etcd

import (
	"fmt"
	"time"

	etcdclient "github.com/coreos/go-etcd/etcd"
	"github.com/golang/glog"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/tools"

	"github.com/openshift/origin/pkg/api/latest"
	configapi "github.com/openshift/origin/pkg/cmd/server/api"
)

// RunEtcd starts an etcd server and runs it forever
func RunEtcd(etcdServerConfig *configapi.EtcdConfig) {
	cfg := &config{
		name: defaultName,
		dir:  etcdServerConfig.StorageDir,

		TickMs:       100,
		ElectionMs:   1000,
		maxSnapFiles: 5,
		maxWalFiles:  5,

		initialClusterToken: "etcd-cluster",
	}
	var err error
	if configapi.UseTLS(etcdServerConfig.ServingInfo) {
		cfg.clientTLSInfo.CAFile = etcdServerConfig.ServingInfo.ClientCA
		cfg.clientTLSInfo.CertFile = etcdServerConfig.ServingInfo.ServerCert.CertFile
		cfg.clientTLSInfo.KeyFile = etcdServerConfig.ServingInfo.ServerCert.KeyFile
	}
	if cfg.lcurls, err = urlsFromStrings(etcdServerConfig.ServingInfo.BindAddress, cfg.clientTLSInfo); err != nil {
		glog.Fatalf("Unable to build etcd client URLs: %v", err)
	}

	if configapi.UseTLS(etcdServerConfig.PeerServingInfo) {
		cfg.peerTLSInfo.CAFile = etcdServerConfig.PeerServingInfo.ClientCA
		cfg.peerTLSInfo.CertFile = etcdServerConfig.PeerServingInfo.ServerCert.CertFile
		cfg.peerTLSInfo.KeyFile = etcdServerConfig.PeerServingInfo.ServerCert.KeyFile
	}
	if cfg.lpurls, err = urlsFromStrings(etcdServerConfig.PeerServingInfo.BindAddress, cfg.peerTLSInfo); err != nil {
		glog.Fatalf("Unable to build etcd peer URLs: %v", err)
	}

	if cfg.acurls, err = urlsFromStrings(etcdServerConfig.Address, cfg.clientTLSInfo); err != nil {
		glog.Fatalf("Unable to build etcd announce client URLs: %v", err)
	}
	if cfg.apurls, err = urlsFromStrings(etcdServerConfig.PeerAddress, cfg.peerTLSInfo); err != nil {
		glog.Fatalf("Unable to build etcd announce peer URLs: %v", err)
	}

	if err := cfg.resolveUrls(); err != nil {
		glog.Fatalf("Unable to resolve etcd URLs: %v", err)
	}

	cfg.initialCluster = fmt.Sprintf("%s=%s", cfg.name, cfg.apurls[0].String())

	stopped, err := startEtcd(cfg)
	if err != nil {
		glog.Fatalf("Unable to start etcd: %v", err)
	}
	go func() {
		glog.Infof("Started etcd at %s", etcdServerConfig.Address)
		<-stopped
	}()
}

// GetAndTestEtcdClient creates an etcd client based on the provided config and waits
// until etcd server is reachable. It errors out and exits if the server cannot
// be reached for a certain amount of time.
func GetAndTestEtcdClient(etcdClientInfo configapi.EtcdConnectionInfo) (*etcdclient.Client, error) {
	var etcdClient *etcdclient.Client

	if len(etcdClientInfo.ClientCert.CertFile) > 0 {
		tlsClient, err := etcdclient.NewTLSClient(
			etcdClientInfo.URLs,
			etcdClientInfo.ClientCert.CertFile,
			etcdClientInfo.ClientCert.KeyFile,
			etcdClientInfo.CA,
		)
		if err != nil {
			return nil, err
		}
		etcdClient = tlsClient
	} else if len(etcdClientInfo.CA) > 0 {
		etcdClient = etcdclient.NewClient(etcdClientInfo.URLs)
		err := etcdClient.AddRootCA(etcdClientInfo.CA)
		if err != nil {
			return nil, err
		}
	} else {
		etcdClient = etcdclient.NewClient(etcdClientInfo.URLs)
	}

	for i := 0; ; i++ {
		// TODO: make sure this works with etcd2 (root key may not exist)
		_, err := etcdClient.Get("/", false, false)
		if err == nil || tools.IsEtcdNotFound(err) {
			break
		}
		if i > 100 {
			return nil, fmt.Errorf("Could not reach etcd: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	return etcdClient, nil
}

// NewOpenShiftEtcdHelper returns an EtcdHelper for the provided arguments or an error if the version
// is incorrect.
func NewOpenShiftEtcdHelper(etcdClientInfo configapi.EtcdConnectionInfo) (helper tools.EtcdHelper, err error) {
	// Connect and setup etcd interfaces
	client, err := GetAndTestEtcdClient(etcdClientInfo)
	if err != nil {
		return tools.EtcdHelper{}, err
	}

	version := latest.Version
	interfaces, err := latest.InterfacesFor(version)
	if err != nil {
		return helper, err
	}
	return tools.NewEtcdHelper(client, interfaces.Codec), nil
}
