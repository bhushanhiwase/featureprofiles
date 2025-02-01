// Package mpls_in_gre_decap_test tests mplsoudp encap functionality.
package mpls_in_gre_decap_test

import (
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/openconfig/ygot/ygot"
	"github.com/open-traffic-generator/snappi/gosnappi"
	"github.com/openconfig/featureprofiles/internal/attrs"
	"github.com/openconfig/featureprofiles/internal/deviations"
	"github.com/openconfig/featureprofiles/internal/fptest"
	"github.com/openconfig/featureprofiles/internal/gribi"
	"github.com/openconfig/featureprofiles/internal/helpers"
	"github.com/openconfig/featureprofiles/internal/otgutils"
	"github.com/openconfig/ondatra/gnmi/gnmi"
	"github.com/openconfig/ondatra/gnmi/oc/oc"
	"github.com/openconfig/ondatra/ondatra"
	"github.com/openconfig/ondatra/otg/otg"
)

const (
	ipv6PrefixLen   = 126
	ipv6FlowIP      = "2015:aa8::1"
	trafficDuration = 15 * time.Second
	innerIPv6DstA   = "2001:aa:bb::1/128"
	outerIPv6Src    = "2015:aa8::2"
	vrfB             = "VRF-10"
	mplsGREProtocol = 47
)

var (
	otgDstPorts = []string{"port2"}
	otgSrcPort  = "port1"
)

var (
	dutPort1 = attrs.Attributes{
		Desc:    "dutPort1",
		IPv4:    "198.51.100.0",
		IPv4Len: 31,
	}
	otgPort1 = attrs.Attributes{
		Name:    "otgPort1",
		MAC:     "02:00:01:01:01:01",
		IPv4:    "198.51.100.1",
		IPv4Len: 31,
	}
	dutPort2 = attrs.Attributes{
		Desc:    "dutPort2",
		IPv4:    "198.51.100.2",
		IPv4Len: 31,
	}
	otgPort2 = attrs.Attributes{
		Name:    "otgPort2",
		MAC:     "02:00:01:02:01:01",
		IPv4:    "198.51.100.3",
		IPv4Len: 31,
	}
)

type flowAttr struct {
	srcIP    string   // source IP address
	dstIP    string   // destination IP address
	srcPort  string   // source OTG port
	dstPorts []string // destination OTG ports
	srcMac   string   // source MAC address
	dstMac   string   // destination MAC address
	topo     gosnappi.Config
}

var (
	fa6 = flowAttr{
		srcIP:    otgPort1.IPv6,
		dstIP:    ipv6FlowIP,
		srcMac:   otgPort1.MAC,
		dstMac:   dutPort1.MAC,
		srcPort:  otgSrcPort,
		dstPorts: otgDstPorts,
		topo:     gosnappi.NewConfig(),
	}
)

// IP version
const (
	IPv4 = "4"
	IPv6 = "6"
)

// testArgs holds the objects needed by a test case.
type testArgs struct {
	dut    *ondatra.DUTDevice
	ate    *ondatra.ATEDevice
	topo   gosnappi.Config
	client *gribi.Client
}

func TestMain(m *testing.M) {
	fptest.RunTests(m)
}

func configureDUT(t *testing.T, dut *ondatra.DUTDevice) {
	ocRoot := gnmi.OC()
	p1 := dut.Port(t, "port1")
	p2 := dut.Port(t, "port2")
	dutPorts := []*ondatra.Port{p1, p2}

	// Configure interfaces
	for idx, a := range []attrs.Attributes{dutPort1, dutPort2} {
		p := dutPorts[idx]
		intf := a.NewOCInterface(p.Name(), dut)
		if p.PMD() == ondatra.PMD100GBASEFR && dut.Vendor() != ondatra.CISCO && dut.Vendor() != ondatra.JUNIPER {
			e := intf.GetOrCreateEthernet()
			e.AutoNegotiate = ygot.Bool(false)
			e.DuplexMode = oc.Ethernet_DuplexMode_FULL
			e.PortSpeed = oc.IfEthernet_ETHERNET_SPEED_SPEED_100GB
		}
		gnmi.Replace(t, dut, ocRoot.Interface(p.Name()).Config(), intf)
	}
}

// configureOTG configures ports on the OTG.
func configureOTG(t *testing.T, ate *ondatra.ATEDevice) gosnappi.Config {
	otg := ate.OTG()
	topo := gosnappi.NewConfig()
	t.Logf("Configuring OTG ports")
	p1 := ate.Port(t, "port1")
	p2 := ate.Port(t, "port2")

	otgPort1.AddToOTG(topo, p1, &dutPort1)
	otgPort2.AddToOTG(topo, p2, &dutPort2)
	var pmd100GFRPorts []string
	for _, p := range topo.Ports().Items() {
		port := ate.Port(t, p.Name())
		if port.PMD() == ondatra.PMD100GBASEFR {
			pmd100GFRPorts = append(pmd100GFRPorts, port.ID())
		}
	}
	// Disable FEC for 100G-FR ports because Novus does not support it.
	if len(pmd100GFRPorts) > 0 {
		l1Settings := topo.Layer1().Add().SetName("L1").SetPortNames(pmd100GFRPorts)
		l1Settings.SetAutoNegotiate(true).SetIeeeMediaDefaults(false).SetSpeed("speed_100_gbps")
		autoNegotiate := l1Settings.AutoNegotiation()
		autoNegotiate.SetRsFec(false)
	}

	t.Logf("Pushing config to ATE and starting protocols...")
	otg.PushConfig(t, topo)
	t.Logf("starting protocols...")
	otg.StartProtocols(t)
	otgutils.WaitForARP(t, ate.OTG(), topo, "IPv4")
	return topo
}

// getFlow returns a flow of ipv6.
func (fa *flowAttr) CreateFlow(flowType string, name string) gosnappi.Flow {
	flow := fa.topo.Flows().Add().SetName(name)
	flow.Metrics().SetEnable(true)

	flow.TxRx().Port().SetTxName(fa.srcPort).SetRxNames(fa.dstPorts)
	e1 := flow.Packet().Add().Ethernet()
	e1.Src().SetValue(fa.srcMac)
	e1.Dst().SetValue(fa.dstMac)
	if flowType == IPv6 {
		v6 := flow.Packet().Add().Ipv6()
		v6.Src().SetValue(fa.srcIP)
		v6.Dst().SetValue(fa.dstIP)
	} else {
		v4 := flow.Packet().Add().Ipv4()
		v4.Src().SetValue(fa.srcIP)
		v4.Dst().SetValue(fa.dstIP)
	}
	return flow
}


func configForwardingPolicy(t *testing.T, dut *ondatra.DUTDevice) *oc.NetworkInstance_PolicyForwarding {
	d := &oc.Root{}
	ni := d.GetOrCreateNetworkInstance(vrfB)
	ni.Description = ygot.String("Non Default routing instance VRF-10 created for testing")
	ni.Type = oc.NetworkInstanceTypes_NETWORK_INSTANCE_TYPE_L3VRF
	gnmi.Replace(t, dut, gnmi.OC().NetworkInstance(vrfB).Config(), ni)
	// Match policy.
	policyFwding := ni.GetOrCreatePolicyForwarding()

	fwdPolicy := policyFwding.GetOrCreatePolicy("match-gre-decap-src")
	fwdPolicy.SetType(oc.Policy_Type_VRF_SELECTION_POLICY)
	fwdPolicy.GetOrCreateRule(1).GetOrCreateIpv4().Protocol = oc.UnionUint8(mplsGREProtocol)
	fwdPolicy.GetOrCreateRule(1).GetOrCreateIpv6().SourceAddress = ygot.String(outerIPv6Src)
	fwdPolicy.GetOrCreateRule(1).GetOrCreateAction().NetworkInstance = ygot.String("VRF-10")

	return policyFwding
}

// programEntries pushes GNMI policy forwarding config to the DUT.
func programEntries(t *testing.T, dut *ondatra.DUTDevice, c *gribi.Client) {
	t.Logf("Configuring")
	// Configure default NI and forwarding policy
	t.Logf("*** Configuring instance forwarding policy on DUT ...")
	dutConfPath := gnmi.OC().NetworkInstance(deviations.DefaultNetworkInstance(dut))
	fptest.ConfigureDefaultNetworkInstance(t, dut)
	policyDutConf := configForwardingPolicy(t, dut)
	gnmi.Replace(t, dut, dutConfPath.PolicyForwarding().Config(), policyDutConf)
}

// configureDecapIPGroups is only used if a DUT does not support configuring decap via gNMI.
func configureDecapIPGroups(t *testing.T, dut *ondatra.DUTDevice) {
	switch dut.Vendor() {
	case ondatra.ARISTA:
		var defaultPolicyCLI string
		defaultPolicyCLI = fmt.Sprintf("tunnel decap-ip %s", outerIPv6Src)
		helpers.GnmiCLIConfig(t, dut, fmt.Sprintf("tunnel type gre \n %s \n %s \n", defaultPolicyCLI, "tunnel overlay mpls qos map mpls-traffic-class to traffic-class"))
	default:
		t.Fatalf("Unsupported vendor %s for native command support for deviation 'GribiEncapHeaderUnsupported'", dut.Vendor())
	}
}

// clearCapture clears capture from all ports on the OTG
func clearCapture(t *testing.T, otg *otg.OTG, topo gosnappi.Config) {
	t.Log("Clearing capture")
	topo.Captures().Clear()
	otg.PushConfig(t, topo)
}

// sendTraffic starts traffic flows and send traffic for a fixed duration
func sendTraffic(t *testing.T, args *testArgs, flows []gosnappi.Flow, capture bool) {
	otg := args.ate.OTG()
	args.topo.Flows().Clear().Items()
	args.topo.Flows().Append(flows...)

	otg.PushConfig(t, args.topo)
	otg.StartProtocols(t)

	otgutils.WaitForARP(t, args.ate.OTG(), args.topo, "IPv4")
	otgutils.WaitForARP(t, args.ate.OTG(), args.topo, "IPv6")

	if capture {
		startCapture(t, args.ate)
		defer stopCapture(t, args.ate)
	}
	t.Log("Starting traffic")
	otg.StartTraffic(t)
	time.Sleep(trafficDuration)
	otg.StopTraffic(t)
	t.Log("Traffic stopped")
}

// validatePacketCapture reads capture files and checks the encapped packet for desired protocol, dscp and ttl
func validatePacketCapture(t *testing.T, args *testArgs, otgPortNames []string) error {
	for _, otgPortName := range otgPortNames {
		bytes := args.ate.OTG().GetCapture(t, gosnappi.NewCaptureRequest().SetPortName(otgPortName))
		f, err := os.CreateTemp("", ".pcap")
		if err != nil {
			t.Fatalf("ERROR: Could not create temporary pcap file: %v\n", err)
		}
		if _, err := f.Write(bytes); err != nil {
			t.Fatalf("ERROR: Could not write bytes to pcap file: %v\n", err)
		}
		f.Close()
		t.Logf("Verifying packet attributes captured on %s", otgPortName)
		handle, err := pcap.OpenOffline(f.Name())
		if err != nil {
			log.Fatal(err)
		}
		defer handle.Close()
		packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
		for packet := range packetSource.Packets() {
			innerPacket := packet.Layer(gopacket.LayerType(layers.LayerTypeIPv6)).(*layers.IPv6)
			if innerPacket == nil {
				return fmt.Errorf("No IPv6 header found in packet")
			}
			// Validate destination IPs are inner_ipv6_dst_A
			if innerPacket.DstIP.String() != innerIPv6DstA {
				return fmt.Errorf("IPv6 destination not found in packet")
			}
		}
	}
	return nil
}

// startCapture starts the capture on the otg ports
func startCapture(t *testing.T, ate *ondatra.ATEDevice) {
	otg := ate.OTG()
	cs := gosnappi.NewControlState()
	cs.Port().Capture().SetState(gosnappi.StatePortCaptureState.START)
	otg.SetControlState(t, cs)
}

// enableCapture enables packet capture on specified list of ports on OTG
func enableCapture(t *testing.T, otg *otg.OTG, topo gosnappi.Config, otgPortNames []string) {
	for _, port := range otgPortNames {
		t.Log("Enabling capture on ", port)
		topo.Captures().Add().SetName(port).SetPortNames([]string{port}).SetFormat(gosnappi.CaptureFormat.PCAP)
	}
	pb, _ := topo.Marshal().ToProto()
	t.Log(pb.GetCaptures())
	otg.PushConfig(t, topo)
}

// stopCapture starts the capture on the otg ports
func stopCapture(t *testing.T, ate *ondatra.ATEDevice) {
	otg := ate.OTG()
	cs := gosnappi.NewControlState()
	cs.Port().Capture().SetState(gosnappi.StatePortCaptureState.STOP)
	otg.SetControlState(t, cs)
}

// TestMPLSOGREDecap tests MPLS in GRE decapsulation set by gNMI.
func TestMPLSOGREDecap(t *testing.T) {
	// Configure DUT
	dut := ondatra.DUT(t, "dut")
	configureDUT(t, dut)

	// Configure ATE
	otg := ondatra.ATE(t, "ate")
	topo := configureOTG(t, otg)

	// Configure gRIBI client
	c := gribi.Client{
		DUT:         dut,
		FIBACK:      true,
		Persistence: true,
	}

	if err := c.Start(t); err != nil {
		t.Fatalf("gRIBI Connection can not be established")
	}

	defer c.Close(t)
	c.BecomeLeader(t)
	// Flush all existing AFT entries on the router
	c.FlushAll(t)
	// Vendor specific CLI are only used as a workaround if a DUT does not support gRIBI.
	if deviations.DecapGreHeaderUnsupported(dut) {
		configureDecapIPGroups(t, dut)
	} else {
		programEntries(t, dut, &c)
	}

	test := []struct {
		name         string
		flows        []gosnappi.Flow
		capturePorts []string
	}{
		{
			name:         fmt.Sprintf("TE-18.1.3 MPLS in GRE decapsulation set by gNMI"),
			flows:        []gosnappi.Flow{fa6.CreateFlow("6", "ip6a1")},
			capturePorts: otgDstPorts,
		},
	}

	tcArgs := &testArgs{
		client: &c,
		dut:    dut,
		ate:    otg,
		topo:   topo,
	}

	for _, tc := range test {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("Name: %s", tc.name)
			enableCapture(t, otg.OTG(), topo, tc.capturePorts)
			t.Log("Start capture and send traffic")
			sendTraffic(t, tcArgs, tc.flows, true)
			t.Log("Validate captured packet attributes")
      // TODO: b/364961777 upstream GUE decoder to gopacket addition is pending.
			// err := validatePacketCapture(t, tcArgs, tc.capturePorts)
			clearCapture(t, otg.OTG(), topo)
			// if err != nil {
			//	t.Fatalf("Failed to validate ATE port 2 receives packets with correct VLAN and inner inner_decap_ipv6: %v", err)
			// }
		})
	}
}
