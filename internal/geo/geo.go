// Package geo resolves client IPs to country and network using optional
// MaxMind-format databases (GeoLite2 / DB-IP "lite").
package geo

import (
	"net"
	"net/netip"

	"github.com/oschwald/maxminddb-golang"
)

// Resolver looks IPs up in up to two mmdb files.
type Resolver struct {
	country *maxminddb.Reader
	asn     *maxminddb.Reader
}

// Open opens the databases. Empty paths are skipped. It returns nil when
// neither database is configured.
func Open(countryPath, asnPath string) (*Resolver, error) {
	r := &Resolver{}
	var err error
	if countryPath != "" {
		if r.country, err = maxminddb.Open(countryPath); err != nil {
			return nil, err
		}
	}
	if asnPath != "" {
		if r.asn, err = maxminddb.Open(asnPath); err != nil {
			r.Close()
			return nil, err
		}
	}
	if r.country == nil && r.asn == nil {
		return nil, nil
	}
	return r, nil
}

// Close releases the databases.
func (r *Resolver) Close() {
	if r == nil {
		return
	}
	if r.country != nil {
		r.country.Close()
	}
	if r.asn != nil {
		r.asn.Close()
	}
}

type countryRec struct {
	Country struct {
		ISO string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

type asnRec struct {
	Number uint   `maxminddb:"autonomous_system_number"`
	Org    string `maxminddb:"autonomous_system_organization"`
}

// Lookup returns country code, ASN and organisation (zero values if unknown).
func (r *Resolver) Lookup(ip string) (country string, asn int, org string) {
	if r == nil {
		return
	}
	a, err := netip.ParseAddr(ip)
	if err != nil || a.IsPrivate() || a.IsLoopback() {
		return
	}
	nip := net.IP(a.AsSlice())
	if r.country != nil {
		var c countryRec
		if r.country.Lookup(nip, &c) == nil {
			country = c.Country.ISO
		}
	}
	if r.asn != nil {
		var n asnRec
		if r.asn.Lookup(nip, &n) == nil {
			asn, org = int(n.Number), n.Org
		}
	}
	return
}
