package inventory

import "testing"

const domstatsSample = `Domain: 'web-01'
  state.state=1
  state.reason=1
  cpu.time=1234567890123
  cpu.user=400000000000
  vcpu.current=4
  vcpu.maximum=8
  balloon.current=4194304
  balloon.maximum=8388608
  balloon.rss=3145728
  block.count=2
  block.0.name=vda
  block.0.rd.bytes=1000
  block.0.wr.bytes=2000
  block.1.name=vdb
  block.1.rd.bytes=500
  block.1.wr.bytes=750
  net.count=2
  net.0.name=vnet0
  net.0.rx.bytes=8000
  net.0.tx.bytes=9000
  net.1.name=vnet1
  net.1.rx.bytes=1000
  net.1.tx.bytes=2000

Domain: 'db-01'
  state.state=5
  vcpu.current=2
  balloon.current=2097152
`

func TestParseDomstatsReadsTheHypervisorView(t *testing.T) {
	domains := parseDomstats(domstatsSample)
	if len(domains) != 2 {
		t.Fatalf("domains = %d, want 2", len(domains))
	}

	web := domains["web-01"]
	if web.State != "running" || web.VCPUs != 4 {
		t.Errorf("state/vcpus = %q/%d", web.State, web.VCPUs)
	}
	if web.CPUTimeNanos != 1234567890123 {
		t.Errorf("cpu time = %d", web.CPUTimeNanos)
	}
	// balloon figures are KiB.
	if web.MemoryBytes != 4194304*1024 || web.MemoryUsedBytes != 3145728*1024 {
		t.Errorf("memory assigned/used = %d/%d", web.MemoryBytes, web.MemoryUsedBytes)
	}
	// Every block and interface is summed, not just the first.
	if web.DiskReadBytes != 1500 || web.DiskWriteBytes != 2750 {
		t.Errorf("disk read/write = %d/%d", web.DiskReadBytes, web.DiskWriteBytes)
	}
	if web.NetworkRxBytes != 9000 || web.NetworkTxBytes != 11000 {
		t.Errorf("network rx/tx = %d/%d", web.NetworkRxBytes, web.NetworkTxBytes)
	}

	// A stopped guest reports no balloon RSS; that must not become a zero
	// masquerading as a measurement.
	db := domains["db-01"]
	if db.State != "shut off" || db.MemoryUsedBytes != 0 || db.MemoryBytes == 0 {
		t.Errorf("db = %+v", db)
	}
}

func TestParseDomstatsIgnoresNoise(t *testing.T) {
	if got := parseDomstats(""); len(got) != 0 {
		t.Fatalf("empty output produced %d domains", len(got))
	}
	if got := parseDomstats("error: failed to connect to the hypervisor"); len(got) != 0 {
		t.Fatalf("an error message produced %d domains", len(got))
	}
	// Keys before any Domain header belong to nothing and are dropped.
	if got := parseDomstats("cpu.time=5\nDomain: 'a'\n  state.state=1\n"); len(got) != 1 || got["a"].CPUTimeNanos != 0 {
		t.Fatalf("stray keys were attributed: %+v", got)
	}
}
