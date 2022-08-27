package sakuracloud

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sacloud/iaas-api-go"
	"github.com/sacloud/iaas-api-go/types"

	log "github.com/sirupsen/logrus"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"
)

const (
	sakuraCloudRecordTTL = 300
)

type SakuraCloudProvider struct {
	provider.BaseProvider
	Client *iaas.Client
	domainFilter endpoint.DomainFilter
	DryRun bool
}

func NewSakuraCloudProvider(domainFilter endpoint.DomainFilter, dryRun bool) (*SakuraCloudProvider, error) {
	token, ok := os.LookupEnv("SAKURACLOUD_ACCESS_TOKEN")
	if !ok {
		return nil, fmt.Errorf("No token found")
	}
	secret, ok := os.LookupEnv("SAKURACLOUD_ACCESS_TOKEN_SECRET")
	if !ok {
		return nil, fmt.Errorf("No secret found")
	}

	client := iaas.NewClient(token,secret)

	provider := &SakuraCloudProvider{
		Client:       client,
		domainFilter: domainFilter,
		DryRun      : dryRun,
	}

	return provider, nil
}

func (p *SakuraCloudProvider) Zones(ctx context.Context) ([]*iaas.DNS, error) {
	var zones []*iaas.DNS
	dnsOp := iaas.NewDNSOp(p.Client)
	findCondition := &iaas.FindCondition{}
	res, err := dnsOp.Find(ctx, findCondition)

	if err != nil {
		return nil, err
	}

	for _, zone := range res.DNS {
		if p.domainFilter.Match(zone.DNSZone) {
			zones = append(zones, zone)
		}
	}

	return zones, nil
}

func (p *SakuraCloudProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	zones, err := p.Zones(ctx)
	if err != nil {
		return nil, err
	}

	var endpoints []*endpoint.Endpoint
	for _, z := range zones {
		for _, r := range z.Records {
			if provider.SupportedRecordType(string(r.Type)) {
				name := r.Name + "." + z.Name
				if r.Name == "@" {
					name = z.Name
				}

				endpoints = append(endpoints, endpoint.NewEndpointWithTTL(name, string(r.Type), endpoint.TTL(r.TTL), r.RData))
			}
		}
	}

	return endpoints, nil
}

func (p *SakuraCloudProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	if changes == nil || len(changes.Create) + len(changes.Delete) + len(changes.UpdateNew) == 0 {
		return nil
	}

	zones, err := p.Zones(ctx)
	if err != nil {
		return err
	}

	for _, z := range zones {
		for _, ep := range changes.Create {
			if !strings.Contains(ep.DNSName, z.Name) {
				continue
			}

			records := p.endpointToRecord(z, ep)
			for _, r := range records {
				log.Debugf(fmt.Sprintf("change.Create: %#v", *r))
				z.Records.Add(r)
			}
		}
	}

	for _, z := range zones {
		for _, ep := range changes.UpdateNew {
			if !strings.Contains(ep.DNSName, z.Name) {
				continue
			}

			records := p.endpointToRecord(z, ep)
			for _, r := range records {
				log.Debugf(fmt.Sprintf("change.UpdateNew: %#v", *r))
				z.Records.Add(r)
			}
		}
	}

	for _, z := range zones {
		for _, ep := range changes.Delete {
			if !strings.Contains(ep.DNSName, z.Name) {
				continue
			}

			records := p.endpointToRecord(z, ep)
			for _, r := range records {
				log.Debugf(fmt.Sprintf("change.Delete: %#v", *r))
				z.Records.Delete(r)
			}
		}
	}

	for _, z := range zones {
		for _, ep := range changes.UpdateOld {
			if !strings.Contains(ep.DNSName, z.Name) {
				continue
			}

			records := p.endpointToRecord(z, ep)
			for _, r := range records {
				log.Debugf(fmt.Sprintf("change.UpdateOld: %#v", *r))
				z.Records.Delete(r)
			}
		}
	}

	if p.DryRun {
		return nil
	}

	dnsOp := iaas.NewDNSOp(p.Client)
	for _, z := range zones {
		if _, err := dnsOp.Update(ctx, z.ID, &iaas.DNSUpdateRequest{Records: z.Records}); err != nil {
			return err
		}
	}

	return nil
}

func (p *SakuraCloudProvider) endpointToRecord(zone *iaas.DNS, ep *endpoint.Endpoint) []*iaas.DNSRecord {
	var records []*iaas.DNSRecord
	var ttl int

	ttl = sakuraCloudRecordTTL
	if ep.RecordTTL.IsConfigured() {
		ttl = int(ep.RecordTTL)
	}

	for _, rdata := range ep.Targets {
		rdata = strings.TrimPrefix(rdata, `"`)
		rdata = strings.TrimSuffix(rdata, `"`)
		records = append(records, iaas.NewDNSRecord(types.EDNSRecordType(ep.RecordType), p.getStrippedRecordName(zone, ep), rdata, ttl))
	}

	return records
}

func (p *SakuraCloudProvider) getStrippedRecordName(zone *iaas.DNS, ep *endpoint.Endpoint) string {
	if ep.DNSName == zone.Name {
		return "@"
	}

	return strings.TrimSuffix(ep.DNSName, "." + zone.Name)
}
