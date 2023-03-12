package main

import (
	"encoding/json"
	"fmt"
	"github.com/ovh/go-ovh/ovh"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"os"
	"time"
)

// Params
type AccessPosttParams struct {
	Ip         string `json:"ip"`
	AccessType string `json:"type"`
}

type AccessIP struct {
	Ip         string `json:"ip"`
	AccessType string `json:"type"`
	AccessId   int    `json:"accessId"`
}

type NasPartition struct {
	Ip    string `json:"ip"`
	Name  string `json:"name"`
	NasHa string `json:"nasha"`
}

func OptionalEnv(envName string, defaultValue string) string {
	env, isSet := os.LookupEnv(envName)
	if !isSet || len(env) == 0 {
		return defaultValue
	}
	return env
}

type NodeWathingController struct {
	informerFactory informers.SharedInformerFactory
	nodeInformer    coreinformers.NodeInformer
	k8sClient       *kubernetes.Clientset
	ovhClient       *ovh.Client
	nasHaPartitions []NasPartition
}

func (c *NodeWathingController) Run(stopCh chan struct{}) error {
	// Starts all the shared informers that have been created by the factory so
	// far.
	c.informerFactory.Start(stopCh)
	// wait for the initial synchronization of the local cache.
	if !cache.WaitForCacheSync(stopCh, c.nodeInformer.Informer().HasSynced) {
		return fmt.Errorf("failed to sync")
	}
	return nil
}

func (c *NodeWathingController) nodeAdd(obj interface{}) {
	node := obj.(*v1.Node)
	logrus.Infof("Node created: %s", node.Name)
	externalIp, err := c.nodeExternalIp(node)
	if err != nil {
		logrus.Error(err)
		return
	}
	for _, partition := range c.nasHaPartitions {
		if !c.isNasPartitionAccessExists(partition, externalIp) {
			logrus.Debugf("%s not in %s/%s access list.", &externalIp, partition.NasHa, partition.Name)
			// node access missing, let's add it.
			params := &AccessPosttParams{Ip: externalIp, AccessType: "readwrite"}
			if err := c.ovhClient.Post(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access", partition.NasHa, partition.Name), &params, nil); err != nil {
				logrus.Errorf("Error addind access to ip %s on %s/%s nasha - %v", externalIp, partition, err)
			}
			logrus.Infof("%s access on %s/%s nasha granted.", externalIp, partition.NasHa, partition.Name)
		}
	}
}

func (c *NodeWathingController) nodeDelete(obj interface{}) {
	node := obj.(*v1.Node)
	logrus.Infof("Node deleted: %s/%s", node.Name)
	externalIp, err := c.nodeExternalIp(node)
	if err != nil {
		logrus.Error(err)
		return
	}
	for _, partition := range c.nasHaPartitions {
		if c.isNasPartitionAccessExists(partition, externalIp) {
			// node no longer exists, delete access
			if err := c.ovhClient.Delete(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access/%s", partition.NasHa, partition.Name, externalIp), nil); err != nil {
				logrus.Errorf("Error deleting access to ip %s on %s/%s nasha - %v", externalIp, partition.NasHa, partition.Name, err)
			}
			logrus.Infof("%s access on %s/%s nasha deleted.", externalIp, partition.NasHa, partition.Name)
		}
	}
}

func (c *NodeWathingController) nodeExternalIp(node *v1.Node) (string, error) {
	for _, a := range node.Status.Addresses {
		if a.Type == v1.NodeExternalIP {
			logrus.Debugf("Node %s external Ip: %s", node.Name, a.Address)
			return a.Address, nil
		}
	}
	return "", fmt.Errorf("Cannot find externale Ip for node %s", node.Name)
}

func (c *NodeWathingController) isNasPartitionAccessExists(part NasPartition, ip string) bool {
	var ipAccess AccessIP
	if err := c.ovhClient.Get(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access/%s", part.NasHa, part.Name, ip), &ipAccess); err != nil {
		logrus.Debugf("Error getting %s nasha ip access on partition %s for ip %s - %v", part.NasHa, part.Name, ip, err)
		return false
	}
	return ipAccess != (AccessIP{})
}

func NasAccessController(informerFactory informers.SharedInformerFactory, k8sClientSet *kubernetes.Clientset, ovhClient *ovh.Client, nasHaPartitions []NasPartition) (*NodeWathingController, error) {
	nodeInformer := informerFactory.Core().V1().Nodes()

	c := &NodeWathingController{
		informerFactory: informerFactory,
		nodeInformer:    nodeInformer,
		ovhClient:       ovhClient,
		k8sClient:       k8sClientSet,
	}
	_, err := nodeInformer.Informer().AddEventHandler(
		// Your custom resource event handlers.
		cache.ResourceEventHandlerFuncs{
			// Called on creation
			AddFunc: c.nodeAdd,
			// Called on resource deletion.
			DeleteFunc: c.nodeDelete,
		},
	)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func main() {

	lvl, ok := os.LookupEnv("LOG_LEVEL")
	// LOG_LEVEL not set, let's default to debug
	if !ok {
		lvl = "info"
	}
	// parse string, this is built-in feature of logrus
	ll, err := logrus.ParseLevel(lvl)
	if err != nil {
		ll = logrus.DebugLevel
	}
	// set global log level
	logrus.SetLevel(ll)

	nasListFile := OptionalEnv("OVH_NASHA_LIST", "/nasha/partitions.json")
	rawNasList, err := ioutil.ReadFile(nasListFile)
	if err != nil {
		logrus.Fatalf("Cannot read %s file: %q", nasListFile, err)
	}

	var partList []NasPartition
	if err := json.Unmarshal(rawNasList, &partList); err != nil {
		logrus.Fatalf("Cannot unmarshal nas list: %q", err)
	}

	ovhClient, err := ovh.NewEndpointClient("ovh-eu")
	if err != nil {
		logrus.Fatalf("Error connecting to OVH API: %q", err)
	}

	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	factory := informers.NewSharedInformerFactory(clientset, time.Hour*24)
	controller, err := NasAccessController(factory, clientset, ovhClient, partList)
	if err != nil {
		logrus.Fatal(err)
	}

	stop := make(chan struct{})
	defer close(stop)
	err = controller.Run(stop)
	if err != nil {
		logrus.Fatal(err)
	}
	select {}
}
