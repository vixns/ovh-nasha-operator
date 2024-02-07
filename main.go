package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"reflect"
	"time"

	"github.com/ovh/go-ovh/ovh"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Params
type AccessPosttParams struct {
	Ip         string `json:"ip"`
	AccessType string `json:"type"`
}

type PartitionAccess struct {
	Ip         string `json:"ip"`
	AccessType string `json:"type"`
	AccessId   int    `json:"accessId"`
}

type NasPartition struct {
	Ip        string `json:"ip"`
	Name      string `json:"name"`
	NasHa     string `json:"nasha"`
	Exclusive bool   `json:"exclusive"`
}

func OptionalEnv(envName string, defaultValue string) string {
	env, isSet := os.LookupEnv(envName)
	if !isSet || len(env) == 0 {
		return defaultValue
	}
	return env
}

func RequiredEnv(envName string) string {
	env, isSet := os.LookupEnv(envName)
	if !isSet || len(env) == 0 {
		logrus.Error("Error: Required env var ", envName, " is missing.")
		os.Exit(1)
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
	// Cleanup exclusive partitions
	nodesIps, err := c.getAllNodesIps()
	if err != nil {
		logrus.Errorf("Cannot get nodes ips, aborting cleanup. %v", err)
	} else {
		for _, p := range c.nasHaPartitions {
			if p.Exclusive {
				c.deleteAllUnkownPartitionAccesses(p, nodesIps)
			}
		}
	}
	// Starts all the shared informers that have been created by the factory so
	// far.
	c.informerFactory.Start(stopCh)
	// wait for the initial synchronization of the local cache.
	if !cache.WaitForCacheSync(stopCh, c.nodeInformer.Informer().HasSynced) {
		return fmt.Errorf("failed to sync")
	}
	return nil
}

func (c *NodeWathingController) Refresh() {
	ips, err := c.getAllNodesIps()
	if err != nil {
		logrus.Errorf("Cannot get nodes ips, aborting refresh. %v", err)
	}
	for _, p := range c.nasHaPartitions {
		for _, ip := range ips {
			if !c.isNasPartitionAccessExists(p, ip) {
				c.addPartitionAccessForIp(p, ip)
			}
		}
	}
}

func (c *NodeWathingController) getAllNodesIps() ([]net.IP, error) {
	nodes, err := c.k8sClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, n := range nodes.Items {
		for _, a := range n.Status.Addresses {
			if a.Type == v1.NodeExternalIP {
				ips = append(ips, net.ParseIP(a.Address))
				break
			}
		}
	}
	return ips, err
}

func (c *NodeWathingController) getPartitonAccessIps(p NasPartition) ([]net.IP, error) {
	logrus.Debugf("Deleting accesses for partition %v", p)
	var accesses []string
	if err := c.ovhClient.Get(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access", p.NasHa, p.Name), &accesses); err != nil {
		return nil, err
	}
	var ips []net.IP
	for _, a := range accesses {
		ip, _, err := net.ParseCIDR(a)
		if err != nil {
			logrus.Errorf("Cannot parse ip %s - %v", a, err)
		}
		ips = append(ips, ip)
	}
	return ips, nil
}

func (c *NodeWathingController) deleteAllUnkownPartitionAccesses(p NasPartition, knownIps []net.IP) {
	ips, err := c.getPartitonAccessIps(p)
	logrus.Debugf("Partition ips: %v", ips)
	if err != nil {
		logrus.Errorf("Cannot get access list for partition %s/%s - %v", p.NasHa, p.Name, err)
	}
	for _, i := range ips {
		var isKownIP = false
		for _, k := range knownIps {
			if i.Equal(k) {
				isKownIP = true
			}
		}
		if !isKownIP {
			c.deletePartitionAccessForIp(p, i)
		}
	}
}

func (c *NodeWathingController) addPartitionAccessForIp(p NasPartition, ip net.IP) {
	logrus.Debugf("add access on %s/%s for ip %s", p.NasHa, p.Name, ip.String())
	params := &AccessPosttParams{Ip: ip.String(), AccessType: "readwrite"}
	if err := c.ovhClient.Post(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access", p.NasHa, p.Name), &params, nil); err != nil {
		logrus.Errorf("Error addind access to ip %s on %s/%s nasha - %v", ip.String(), p, err)
	}
	logrus.Infof("%s access on %s/%s nasha granted.", ip.String(), p.NasHa, p.Name)
}

func (c *NodeWathingController) deletePartitionAccessForIp(p NasPartition, ip net.IP) {
	logrus.Debugf("delete access on %s/%s for ip %s", p.NasHa, p.Name, ip.String())
	if err := c.ovhClient.Delete(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access/%s", p.NasHa, p.Name, ip.String()), nil); err != nil {
		logrus.Errorf("Error deleting access to ip %s on %s/%s nasha - %v", ip.String(), p.NasHa, p.Name, err)
	}
	logrus.Infof("%s access on %s/%s nasha deleted.", p, p.NasHa, p.Name)
}

func (c *NodeWathingController) nodeAdd(obj interface{}) {
	node := obj.(*v1.Node)
	_, ok := node.Labels["node-role.kubernetes.io/control-plane"]
	if ok {
		return
	}
	logrus.Infof("Node created: %s", node.Name)
	ip, err := c.nodeIp(node)
	if err != nil {
		logrus.Error(err)
		return
	}
	for _, partition := range c.nasHaPartitions {
		if !c.isNasPartitionAccessExists(partition, ip) {
			logrus.Debugf("%s not in %s/%s access list.", &ip, partition.NasHa, partition.Name)
			// node access missing, let's add it.
			c.addPartitionAccessForIp(partition, ip)
		}
	}
}

func (c *NodeWathingController) nodeDelete(obj interface{}) {
	node := obj.(*v1.Node)
	logrus.Infof("Node deleted: %s/%s", node.Name)
	ip, err := c.nodeIp(node)
	if err != nil {
		logrus.Error(err)
		return
	}
	for _, partition := range c.nasHaPartitions {
		if c.isNasPartitionAccessExists(partition, ip) {
			// node no longer exists, delete access
			c.deletePartitionAccessForIp(partition, ip)
		}
	}
}

func (c *NodeWathingController) nodeIp(node *v1.Node) (net.IP, error) {
	for _, a := range node.Status.Addresses {
		if a.Type == v1.NodeExternalIP {
			logrus.Debugf("Node %s external Ip: %s", node.Name, a.Address)
			return net.ParseIP(a.Address), nil
		}
	}
	// Try internalIps if no externalIps
	for _, a := range node.Status.Addresses {
		if a.Type == v1.NodeInternalIP {
			logrus.Debugf("Node %s internal Ip: %s", node.Name, a.Address)
			return net.ParseIP(a.Address), nil
		}
	}
	return nil, fmt.Errorf("Cannot find external or external Ip for node %s", node.Name)
}


func (c *NodeWathingController) isNasPartitionAccessExists(part NasPartition, ip net.IP) bool {
	var ipAccess PartitionAccess
	if err := c.ovhClient.Get(fmt.Sprintf("/dedicated/nasha/%s/partition/%s/access/%s", part.NasHa, part.Name, ip.String()), &ipAccess); err != nil {
		logrus.Debugf("Error getting %s nasha ip access on partition %s for ip %s - %v", part.NasHa, part.Name, ip.String(), err)
		return false
	}
	return ipAccess != (PartitionAccess{})
}

func NasAccessController(informerFactory informers.SharedInformerFactory, k8sClientSet *kubernetes.Clientset, ovhClient *ovh.Client) (*NodeWathingController, error) {
	nodeInformer := informerFactory.Core().V1().Nodes()

	c := &NodeWathingController{
		informerFactory: informerFactory,
		nodeInformer:    nodeInformer,
		ovhClient:       ovhClient,
		k8sClient:       k8sClientSet,
		nasHaPartitions: nil,
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

	ovhClient, err := ovh.NewEndpointClient("ovh-eu")
	if err != nil {
		logrus.Fatalf("Error connecting to OVH API: %v", err)
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
	controller, err := NasAccessController(factory, clientset, ovhClient)
	if err != nil {
		logrus.Fatal(err)
	}

	listOptions := metav1.ListOptions{}
	namespace := RequiredEnv("K8S_NAMESPACE")
	watcher, err := clientset.CoreV1().ConfigMaps(namespace).Watch(context.TODO(), listOptions)
	if err != nil {
		logrus.Fatalf("Cannot watch configmaps: %v", err)
	}

	stop := make(chan struct{})
	defer close(stop)
	for {
		select {
		case event, ok := <-watcher.ResultChan():
			if !ok {
				logrus.Error("watcher stopped")
				watcher, err = clientset.CoreV1().ConfigMaps(namespace).Watch(context.TODO(), listOptions)
				logrus.Info("start new watcher")
				if err != nil {
					logrus.Errorf("Cannot watch configmaps: %v", err)
				}
				break
			}
			logrus.Debugf("Watch event: %v", event)
			configMap, ok := event.Object.(*v1.ConfigMap)
			if !ok {
				logrus.Fatal("unexpected type")
				continue
			}
			if configMap.Name == "ovh-nasha" {
				// parse the configmap
				var partList []NasPartition
				rawData := []byte(configMap.Data["partitions.json"])
				if err := json.Unmarshal(rawData, &partList); err != nil {
					logrus.Fatalf("Cannot unmarshal nas list: %v", err)
				}
				if controller.nasHaPartitions == nil {
					// first Event, start the controller
					controller.nasHaPartitions = partList
					err = controller.Run(stop)
					if err != nil {
						logrus.Fatal(err)
					}
				} else if !reflect.DeepEqual(partList, controller.nasHaPartitions) {
					controller.nasHaPartitions = partList
					// refresh all accesses
					controller.Refresh()
				}
			}
		}
	}
}
