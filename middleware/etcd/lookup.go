package etcd

import (
	"math"
	"net"

	"github.com/miekg/coredns/middleware"
	"github.com/miekg/coredns/middleware/etcd/msg"
	"github.com/miekg/dns"
)

// need current zone argument.

func (e Etcd) AddressRecords(zone string, state middleware.State, previousRecords []dns.RR) (records []dns.RR, err error) {
	services, err := e.Records(state.Name(), false)
	if err != nil {
		return nil, err
	}

	services = msg.Group(services)

	for _, serv := range services {
		ip := net.ParseIP(serv.Host)
		switch {
		case ip == nil:
			// Try to resolve as CNAME if it's not an IP, but only if we don't create loops.
			// TODO(miek): lowercasing, use Match in middleware/
			if state.Name() == dns.Fqdn(serv.Host) {
				// x CNAME x is a direct loop, don't add those
				continue
			}

			newRecord := serv.NewCNAME(state.QName(), dns.Fqdn(serv.Host))
			if len(previousRecords) > 7 {
				// don't add it, and just continue
				continue
			}
			if isDuplicateCNAME(newRecord, previousRecords) {
				continue
			}

			// Fucks up recursion, need to define this in the function
			// are use another var
			state.Req.Question[0] = dns.Question{Name: dns.Fqdn(serv.Host), Qtype: state.QType(), Qclass: state.QClass()}
			nextRecords, err := e.AddressRecords(zone, state, append(previousRecords, newRecord))
			if err == nil {
				// Only have we found something we should add the CNAME and the IP addresses.
				if len(nextRecords) > 0 {
					records = append(records, newRecord)
					records = append(records, nextRecords...)
				}
				continue
			}
			// This means we can not complete the CNAME, try to look else where.
			target := newRecord.Target
			if dns.IsSubDomain(zone, target) {
				// We should already have found it
				continue
			}
			m1, e1 := e.Proxy.Lookup(state, target, state.QType())
			if e1 != nil {
				continue
			}
			// Len(m1.Answer) > 0 here is well?
			records = append(records, newRecord)
			records = append(records, m1.Answer...)
			continue
		case ip.To4() != nil && (state.QType() == dns.TypeA):
			records = append(records, serv.NewA(state.QName(), ip.To4()))
		case ip.To4() == nil && (state.QType() == dns.TypeAAAA):
			records = append(records, serv.NewAAAA(state.QName(), ip.To16()))
		}
	}
	return records, nil
}

// SRVRecords returns SRV records from etcd.
// If the Target is not a name but an IP address, a name is created on the fly.
func (e Etcd) SRVRecords(zone string, state middleware.State) (records []dns.RR, extra []dns.RR, err error) {
	services, err := e.Records(name, false)
	if err != nil {
		return nil, nil, err
	}

	services = msg.Group(services)

	// Looping twice to get the right weight vs priority
	w := make(map[int]int)
	for _, serv := range services {
		weight := 100
		if serv.Weight != 0 {
			weight = serv.Weight
		}
		if _, ok := w[serv.Priority]; !ok {
			w[serv.Priority] = weight
			continue
		}
		w[serv.Priority] += weight
	}
	lookup := make(map[string]bool)
	for _, serv := range services {
		w1 := 100.0 / float64(w[serv.Priority])
		if serv.Weight == 0 {
			w1 *= 100
		} else {
			w1 *= float64(serv.Weight)
		}
		weight := uint16(math.Floor(w1))
		ip := net.ParseIP(serv.Host)
		switch {
		case ip == nil:
			srv := serv.NewSRV(state.QName(), weight)
			records = append(records, srv)

			if _, ok := lookup[srv.Target]; ok {
				break
			}

			lookup[srv.Target] = true

			if !dns.IsSubDomain(zone, srv.Target) {
				m1, e1 := e.Proxy.Lookup(state, srv.Target, dns.TypeA)
				if e1 == nil {
					extra = append(extra, m1.Answer...)
				}
				m1, e1 = e.Proxy.Lookup(state, srv.Target, dns.TypeAAAA)
				if e1 == nil {
					// If we have seen CNAME's we *assume* that they are already added.
					for _, a := range m1.Answer {
						if _, ok := a.(*dns.CNAME); !ok {
							extra = append(extra, a)
						}
					}
				}
				break
			}
			// Internal name, we should have some info on them, either v4 or v6
			// Clients expect a complete answer, because we are a recursor in their
			// view.
			addr, e1 := e.AddressRecords(dns.Question{srv.Target, dns.ClassINET, dns.TypeA},
				srv.Target, nil, bufsize, dnssec, true)
			if e1 == nil {
				extra = append(extra, addr...)
			}
		case ip.To4() != nil:
			serv.Host = msg.Domain(serv.Key)
			srv := serv.NewSRV(q.Name, weight)

			records = append(records, srv)
			extra = append(extra, serv.NewA(srv.Target, ip.To4()))
		case ip.To4() == nil:
			serv.Host = msg.Domain(serv.Key)
			srv := serv.NewSRV(q.Name, weight)

			records = append(records, srv)
			extra = append(extra, serv.NewAAAA(srv.Target, ip.To16()))
		}
	}
	return records, extra, nil
}

// MXRecords returns MX records from etcd.
// If the Target is not a name but an IP address, a name is created on the fly.
func (e Etcd) MXRecords(zone string, state middleware.State) (records []dns.RR, extra []dns.RR, err error) {
	services, err := e.Records(name, false)
	if err != nil {
		return nil, nil, err
	}

	lookup := make(map[string]bool)
	for _, serv := range services {
		if !serv.Mail {
			continue
		}
		ip := net.ParseIP(serv.Host)
		switch {
		case ip == nil:
			mx := serv.NewMX(q.Name)
			records = append(records, mx)
			if _, ok := lookup[mx.Mx]; ok {
				break
			}

			lookup[mx.Mx] = true

			if !dns.IsSubDomain(s.config.Domain, mx.Mx) {
				m1, e1 := e.Proxy.Lookup(state, mx.Mx, dns.TypeA)
				if e1 == nil {
					extra = append(extra, m1.Answer...)
				}
				m1, e1 = e.Proxy.Lookup(state, mx.Mx, dns.TypeAAAA)
				if e1 == nil {
					// If we have seen CNAME's we *assume* that they are already added.
					for _, a := range m1.Answer {
						if _, ok := a.(*dns.CNAME); !ok {
							extra = append(extra, a)
						}
					}
				}
				break
			}
			// Internal name
			addr, e1 := s.AddressRecords(dns.Question{mx.Mx, dns.ClassINET, dns.TypeA},
				mx.Mx, nil, bufsize, dnssec, true)
			if e1 == nil {
				extra = append(extra, addr...)
			}
		case ip.To4() != nil:
			serv.Host = msg.Domain(serv.Key)
			records = append(records, serv.NewMX(q.Name))
			extra = append(extra, serv.NewA(serv.Host, ip.To4()))
		case ip.To4() == nil:
			serv.Host = msg.Domain(serv.Key)
			records = append(records, serv.NewMX(q.Name))
			extra = append(extra, serv.NewAAAA(serv.Host, ip.To16()))
		}
	}
	return records, extra, nil
}

func (e Etcd) CNAMERecords(zone string, state middleware.State) (records []dns.RR, err error) {
	services, err := e.Records(name, true)
	if err != nil {
		return nil, err
	}

	services = msg.Group(services)

	if len(services) > 0 {
		serv := services[0]
		if ip := net.ParseIP(serv.Host); ip == nil {
			records = append(records, serv.NewCNAME(q.Name, dns.Fqdn(serv.Host)))
		}
	}
	return records, nil
}

func (e Etcd) TXTRecords(zone string, state middleware.State) (records []dns.RR, err error) {
	services, err := e.Records(state.Name(), false)
	if err != nil {
		return nil, err
	}

	services = msg.Group(services)

	for _, serv := range services {
		if serv.Text == "" {
			continue
		}
		records = append(records, serv.NewTXT(q.Name))
	}
	return records, nil
}

func isDuplicateCNAME(r *dns.CNAME, records []dns.RR) bool {
	for _, rec := range records {
		if v, ok := rec.(*dns.CNAME); ok {
			if v.Target == r.Target {
				return true
			}
		}
	}
	return false
}

/*
// Move to state.go somehow?
func (s *server) NameError(req *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetRcode(req, dns.RcodeNameError)
	m.Ns = []dns.RR{s.NewSOA()}
	m.Ns[0].Header().Ttl = s.config.MinTtl
	return m
}

// etcNameError return a NameError to the client if the error
// returned from etcd has ErrorCode == 100.
func isEtcdNameError(err error, s *server) bool {
	if e, ok := err.(etcd.Error); ok && e.Code == etcd.ErrorCodeKeyNotFound {
		return true
	}
	if err != nil {
		logf("error from backend: %s", err)
	}
	return false
}
*/