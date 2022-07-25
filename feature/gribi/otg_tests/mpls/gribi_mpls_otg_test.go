// Package gRIBI MPLS Dataplane Test implements tests of the MPLS dataplane that
// use gRIBI as the programming mechanism.
package gribi_mpls_dataplane_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/open-traffic-generator/snappi/gosnappi"
	mplscompliance "github.com/openconfig/featureprofiles/feature/gribi/tests/mpls"
	"github.com/openconfig/featureprofiles/internal/attrs"
	"github.com/openconfig/featureprofiles/internal/fptest"
	"github.com/openconfig/featureprofiles/internal/otgutils"
	"github.com/openconfig/gribigo/fluent"
	"github.com/openconfig/ondatra"
	"github.com/openconfig/ondatra/telemetry"
	otgtelemetry "github.com/openconfig/ondatra/telemetry/otg"
	"github.com/openconfig/ygot/ygot"
)

const (
	defNIName         = "default"
	baseLabel         = 42
	destinationLabel  = 100
	maximumStackDepth = 20
)

var (
	// ateSrc describes the configuration parameters for the ATE port sourcing
	// a flow.
	ateSrc = &attrs.Attributes{
		Name:    "port1",
		Desc:    "ATE_SRC_PORT",
		IPv4:    "192.0.2.0",
		IPv4Len: 31,
		MAC:     "02:00:01:01:01:01",
		IPv6:    "2001:db8::0",
		IPv6Len: 127,
	}
	// dutSrc describes the configuration parameters for the DUT port connected
	// to the ATE src port.
	dutSrc = &attrs.Attributes{
		Desc:    "DUT_SRC_PORT",
		IPv4:    "192.0.2.1",
		IPv4Len: 31,
		IPv6:    "2001:db8::1",
		IPv6Len: 127,
	}
	// ateDst describes the configuration parameters for the ATE port that acts
	// as the traffic sink.
	ateDst = &attrs.Attributes{
		Name:    "port2",
		Desc:    "ATE_DST_PORT",
		IPv4:    "192.0.2.2",
		IPv4Len: 31,
		MAC:     "02:00:02:01:01:01",
		IPv6:    "2001:db8::2",
		IPv6Len: 127,
	}
	// dutDst describes the configuration parameters for the DUT port that is
	// connected to the ate destination port.
	dutDst = &attrs.Attributes{
		Desc:    "DUT_DST_PORT",
		IPv4:    "192.0.2.3",
		IPv4Len: 31,
		IPv6:    "2001:db8::3",
		IPv6Len: 127,
	}
)

func TestMain(m *testing.M) {
	fptest.RunTests(m)
}

// TODO(robjs):  Test cases to write:
//	* push(N) labels, N = 1-20.
//	* pop(1) - terminating action
//	* pop(1) + push(N)
//	* pop(all) + push(N)

// dutIntf generates the configuration for an interface on the DUT in OpenConfig.
// It returns the generated configuration, or an error if the config could not be
// generated.
func dutIntf(intf *attrs.Attributes) (*telemetry.Interface, error) {
	if intf == nil {
		return nil, fmt.Errorf("invalid nil interface, %v", intf)
	}

	i := &telemetry.Interface{
		Name:        ygot.String(intf.Name),
		Description: ygot.String(intf.Desc),
		Type:        telemetry.IETFInterfaces_InterfaceType_ethernetCsmacd,
		Enabled:     ygot.Bool(true),
	}
	v4 := i.GetOrCreateSubinterface(0).GetOrCreateIpv4()
	v4.Enabled = ygot.Bool(true)
	v4Addr := v4.GetOrCreateAddress(intf.IPv4)
	v4Addr.PrefixLength = ygot.Uint8(intf.IPv4Len)
	return i, nil
}

// configureATEInterfaces configures all the interfaces of the ATE according to the
// supplied ports (srcATE, srcDUT, dstATE, dstDUT) attributes. It returns the gosnappi
// OTG configuration that was applied to the ATE, or an error.
func configureATEInterfaces(t *testing.T, ate *ondatra.ATEDevice, srcATE, srcDUT, dstATE, dstDUT *attrs.Attributes) (gosnappi.Config, error) {
	otg := ate.OTG()
	topology := otg.NewConfig(t)
	for _, p := range []struct {
		ate, dut *attrs.Attributes
	}{
		{ate: srcATE, dut: srcDUT},
		{ate: dstATE, dut: dstDUT},
	} {
		topology.Ports().Add().SetName(p.ate.Name)
		dev := topology.Devices().Add().SetName(p.ate.Name)
		eth := dev.Ethernets().Add().SetName(fmt.Sprintf("%s_ETH", p.ate.Name))
		eth.SetPortName(dev.Name()).SetMac(p.ate.MAC)
		ip := eth.Ipv4Addresses().Add().SetName(fmt.Sprintf("%s_IPV4", dev.Name()))
		ip.SetAddress(p.ate.IPv4).SetGateway(p.dut.IPv4).SetPrefix(int32(p.ate.IPv4Len))

		ip6 := eth.Ipv6Addresses().Add().SetName(fmt.Sprintf("%s_IPV6", dev.Name()))
		ip6.SetAddress(p.ate.IPv6).SetGateway(p.dut.IPv6).SetPrefix(int32(p.ate.IPv6Len))
	}

	otg.PushConfig(t, topology)
	otg.StartProtocols(t)
	return topology, nil
}

// TestMPLSLabelPushDepth validates the gRIBI actions that are used to push N labels onto
// as part of routing towards a next-hop. Note that this test does not validate against the
// dataplane, but solely the gRIBI control-plane support.
func TestMPLSLabelPushDepth(t *testing.T) {
	dut := ondatra.DUT(t, "dut")
	// update our interface specifications with the allocated ports.
	dutSrc.Name = dut.Port(t, "port1").Name()
	dutDst.Name = dut.Port(t, "port2").Name()

	ate := ondatra.ATE(t, "ate")
	testTopo, err := configureATEInterfaces(t, ate, ateSrc, dutSrc, ateDst, dutDst)
	if err != nil {
		t.Fatalf("cannot configure ATE interfaces via OTG, %v", err)
	}

	// configure ports on the DUT.
	for _, i := range []*attrs.Attributes{dutSrc, dutDst} {
		cfg, err := dutIntf(i)
		if err != nil {
			t.Fatalf("cannot generate configuration for interface %s, err: %v", i.Name, err)
		}
		dut.Config().Interface(i.Name).Replace(t, cfg)
	}

	gribic := dut.RawAPIs().GRIBI().Default(t)
	c := fluent.NewClient()
	c.Connection().WithStub(gribic)

	testMPLSFlow := func(t *testing.T, _ []uint32) {
		// We configure a traffic flow from ateSrc -> ateDst (passes through
		// ateSrc -> [ dutSrc -- dutDst ] --> ateDst.
		//
		// Since EgressLabelStack pushes N labels but has a label forwarding
		// entry of 100 that points at that next-hop, we only need this value
		// to check whether traffic is forwarded.
		//
		// TODO(robjs): in the future, extend this test to check that the
		// received label stack is as we expected.

		// wait for ARP to resolve.
		otg := ate.OTG()
		otg.Telemetry().InterfaceAny().Ipv4NeighborAny().LinkLayerAddress().Watch(
			t, time.Minute, func(val *otgtelemetry.QualifiedString) bool {
				return val.IsPresent()
			}).Await(t)

		dstMAC := otg.Telemetry().Interface(fmt.Sprintf("%s_ETH", ateSrc.Name)).Ipv4Neighbor(dutSrc.IPv4).LinkLayerAddress().Get(t)

		// Remove any stale flows.
		testTopo.Flows().Clear().Items()
		mplsFlow := testTopo.Flows().Add().SetName("MPLS_FLOW")
		mplsFlow.Metrics().SetEnable(true)
		mplsFlow.TxRx().Port().SetTxName(ateSrc.Name).SetRxName(ateDst.Name)

		// Set up ethernet layer.
		eth := mplsFlow.Packet().Add().Ethernet()
		eth.Src().SetValue(ateSrc.MAC)
		eth.Dst().SetChoice("value").SetValue(dstMAC)

		// Set up MPLS layer with destination label 100.
		mpls := mplsFlow.Packet().Add().Mpls()
		mpls.Label().SetChoice("value").SetValue(destinationLabel)
		mpls.BottomOfStack().SetChoice("value").SetValue(1)

		otg.PushConfig(t, testTopo)

		t.Logf("Starting MPLS traffic...")
		otg.StartTraffic(t)
		time.Sleep(15 * time.Second)
		t.Logf("Stopping MPLS traffic...")
		otg.StopTraffic(t)

		otgutils.LogPortMetrics(t, otg, testTopo)
	}

	baseLabel := 42
	for i := 1; i <= maximumStackDepth; i++ {
		t.Run(fmt.Sprintf("push %d labels", i), func(t *testing.T) {
			mplscompliance.EgressLabelStack(t, c, defNIName, baseLabel, i, testMPLSFlow)
		})
	}
}
