// Copyright 2020 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package spec

import (
	"context"
	"crypto/tls"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/pingcap/tiup/pkg/cluster/ctxt"
	"github.com/pingcap/tiup/pkg/cluster/spec"
	"github.com/pingcap/tiup/pkg/cluster/template/scripts"
	"github.com/pingcap/tiup/pkg/meta"
	"github.com/pingcap/tiup/pkg/utils"
)

// Components names supported by TiUP
const (
	ComponentTEMWorker    = spec.ComponentTEMWorker
	ComponentPrometheus   = spec.ComponentPrometheus
	ComponentGrafana      = spec.ComponentGrafana
	ComponentAlertmanager = spec.ComponentAlertmanager
)

type (
	// InstanceSpec represent a instance specification
	InstanceSpec interface {
		Role() string
		SSH() (string, int)
		GetMainPort() int
		IsImported() bool
		IgnoreMonitorAgent() bool
	}
)

// Component represents a component of the cluster.
type Component = spec.Component

// Instance represents an instance
type Instance = spec.Instance

// TEMWorkerComponent represents TEM worker component.
type TEMWorkerComponent struct{ Topology *Specification }

// Name implements Component interface.
func (c *TEMWorkerComponent) Name() string {
	return ComponentTEMWorker
}

// Role implements Component interface.
func (c *TEMWorkerComponent) Role() string {
	return ComponentTEMWorker
}

// Instances implements Component interface.
func (c *TEMWorkerComponent) Instances() []Instance {
	ins := make([]Instance, 0)
	for _, s := range c.Topology.Workers {
		s := s
		ins = append(ins, &WorkerInstance{
			Name: s.Name,
			BaseInstance: spec.BaseInstance{
				InstanceSpec: s,
				Name:         c.Name(),
				Host:         s.Host,
				ManageHost:   s.ManageHost,
				Port:         s.Port,
				SSHP:         s.SSHPort,

				Ports: []int{
					s.Port,
				},
				Dirs: []string{
					s.DeployDir,
					s.DataDir,
				},
				StatusFn: s.Status,
				UptimeFn: func(_ context.Context, timeout time.Duration, tlsCfg *tls.Config) time.Duration {
					return spec.UptimeByHost(s.Host, s.Port, timeout, tlsCfg)
				},
			},
			topo: c.Topology,
		})
	}

	return ins
}

// WorkerInstance represent the TEM worker instance
type WorkerInstance struct {
	Name string
	spec.BaseInstance
	topo *Specification
}

// InitConfig implement Instance interface
func (i *WorkerInstance) InitConfig(
	ctx context.Context,
	e ctxt.Executor,
	clusterName,
	clusterVersion,
	deployUser string,
	paths meta.DirPaths,
) error {
	if err := i.BaseInstance.InitConfig(ctx, e, i.topo.GlobalOptions, deployUser, paths); err != nil {
		return err
	}

	enableTLS := i.topo.GlobalOptions.TLSEnabled
	spec := i.InstanceSpec.(*WorkerSpec)

	cfg := &scripts.TEMWorkerScript{
		Name:          i.Name,
		WorkerAddr:    utils.JoinHostPort(i.GetListenHost(), spec.Port),
		AdvertiseAddr: utils.JoinHostPort(spec.Host, spec.Port),

		DeployDir: paths.Deploy,
		LogDir:    paths.Log,
		NumaNode:  spec.NumaNode,
	}

	fp := filepath.Join(paths.Cache, fmt.Sprintf("run_dm-worker_%s_%d.sh", i.GetHost(), i.GetPort()))
	if err := cfg.ConfigToFile(fp); err != nil {
		return err
	}
	dst := filepath.Join(paths.Deploy, "scripts", "run_dm-worker.sh")

	if err := e.Transfer(ctx, fp, dst, false, 0, false); err != nil {
		return err
	}

	_, _, err := e.Execute(ctx, "chmod +x "+dst, false)
	if err != nil {
		return err
	}

	if spec.Config, err = i.setTLSConfig(ctx, enableTLS, spec.Config, paths); err != nil {
		return err
	}

	specConfig := spec.Config
	return i.MergeServerConfig(ctx, e, i.topo.ServerConfigs.Worker, specConfig, paths)
}

// setTLSConfig set TLS Config to support enable/disable TLS
// workrsInstance no need to configure TLS
func (i *WorkerInstance) setTLSConfig(ctx context.Context, enableTLS bool, configs map[string]any, paths meta.DirPaths) (map[string]any, error) {
	// set TLS configs
	if enableTLS {
		if configs == nil {
			configs = make(map[string]any)
		}
		configs["ssl-ca"] = fmt.Sprintf(
			"%s/tls/%s",
			paths.Deploy,
			"ca.crt",
		)
		configs["ssl-cert"] = fmt.Sprintf(
			"%s/tls/%s.crt",
			paths.Deploy,
			i.Role())
		configs["ssl-key"] = fmt.Sprintf(
			"%s/tls/%s.pem",
			paths.Deploy,
			i.Role())
	} else {
		// dm-worker tls config list
		tlsConfigs := []string{
			"ssl-ca",
			"ssl-cert",
			"ssl-key",
		}
		// delete TLS configs
		if configs != nil {
			for _, config := range tlsConfigs {
				delete(configs, config)
			}
		}
	}

	return configs, nil
}

// ScaleConfig deploy temporary config on scaling
func (i *WorkerInstance) ScaleConfig(
	ctx context.Context,
	e ctxt.Executor,
	topo spec.Topology,
	clusterName,
	clusterVersion,
	deployUser string,
	paths meta.DirPaths,
) error {
	s := i.topo
	defer func() {
		i.topo = s
	}()
	i.topo = topo.(*Specification)
	return i.InitConfig(ctx, e, clusterName, clusterVersion, deployUser, paths)
}

// GetGlobalOptions returns cluster topology
func (topo *Specification) GetGlobalOptions() spec.GlobalOptions {
	return topo.GlobalOptions
}

// GetMonitoredOptions returns MonitoredOptions
func (topo *Specification) GetMonitoredOptions() *spec.MonitoredOptions {
	return topo.MonitoredOptions
}

// ComponentsByStopOrder return component in the order need to stop.
func (topo *Specification) ComponentsByStopOrder() (comps []Component) {
	comps = topo.ComponentsByStartOrder()
	// revert order
	i := 0
	j := len(comps) - 1
	for i < j {
		comps[i], comps[j] = comps[j], comps[i]
		i++
		j--
	}
	return
}

// ComponentsByStartOrder return component in the order need to start.
func (topo *Specification) ComponentsByStartOrder() (comps []Component) {
	// "dm-master", "dm-worker"
	comps = append(comps, &TEMWorkerComponent{topo})
	comps = append(comps, &spec.MonitorComponent{Topology: topo}) // prometheus
	comps = append(comps, &spec.GrafanaComponent{Topology: topo})
	comps = append(comps, &spec.AlertManagerComponent{Topology: topo})
	return
}

// ComponentsByUpdateOrder return component in the order need to be updated.
func (topo *Specification) ComponentsByUpdateOrder() (comps []Component) {
	// "dm-master", "dm-worker"
	comps = append(comps, &TEMWorkerComponent{topo})
	comps = append(comps, &spec.MonitorComponent{Topology: topo})
	comps = append(comps, &spec.GrafanaComponent{Topology: topo})
	comps = append(comps, &spec.AlertManagerComponent{Topology: topo})
	return
}

// IterComponent iterates all components in component starting order
func (topo *Specification) IterComponent(fn func(comp Component)) {
	for _, comp := range topo.ComponentsByStartOrder() {
		fn(comp)
	}
}

// IterInstance iterates all instances in component starting order
func (topo *Specification) IterInstance(fn func(instance Instance), concurrency ...int) {
	maxWorkers := 1
	wg := sync.WaitGroup{}
	if len(concurrency) > 0 && concurrency[0] > 1 {
		maxWorkers = concurrency[0]
	}
	workerPool := make(chan struct{}, maxWorkers)

	for _, comp := range topo.ComponentsByStartOrder() {
		for _, inst := range comp.Instances() {
			wg.Add(1)
			workerPool <- struct{}{}
			go func(inst Instance) {
				defer func() {
					<-workerPool
					wg.Done()
				}()
				fn(inst)
			}(inst)
		}
	}
	wg.Wait()
}

// IterHost iterates one instance for each host
func (topo *Specification) IterHost(fn func(instance Instance)) {
	hostMap := make(map[string]bool)
	for _, comp := range topo.ComponentsByStartOrder() {
		for _, inst := range comp.Instances() {
			host := inst.GetHost()
			_, ok := hostMap[host]
			if !ok {
				hostMap[host] = true
				fn(inst)
			}
		}
	}
}
