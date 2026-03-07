package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// EnvironmentReconciler reconciles an Environment object.
type EnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps;secrets;services;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

func (r *EnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	env := &openvoxv1alpha1.Environment{}
	if err := r.Get(ctx, req.NamespacedName, env); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if env.Status.Phase == "" {
		env.Status.Phase = openvoxv1alpha1.EnvironmentPhasePending
		if err := r.Status().Update(ctx, env); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Step 1: Reconcile ConfigMaps
	logger.Info("reconciling ConfigMaps")
	if err := r.reconcileConfigMap(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling ConfigMaps: %w", err)
	}
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionConfigReady,
		Status:             metav1.ConditionTrue,
		Reason:             "ConfigMapsCreated",
		Message:            "Configuration ConfigMaps are up to date",
		LastTransitionTime: metav1.Now(),
	})

	// Step 2: CA lifecycle
	// Step 2a: Ensure CA PVC exists
	if err := r.reconcileCAPVC(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA PVC: %w", err)
	}

	// Step 2b: Check if CA is initialized (marker Secret exists)
	caSecretName := fmt.Sprintf("%s-ca", env.Name)
	caSecret := &corev1.Secret{}
	caInitialized := true
	if err := r.Get(ctx, types.NamespacedName{Name: caSecretName, Namespace: env.Namespace}, caSecret); err != nil {
		if errors.IsNotFound(err) {
			caInitialized = false
		} else {
			return ctrl.Result{}, err
		}
	}

	if !caInitialized {
		logger.Info("CA not initialized, running setup job")
		env.Status.Phase = openvoxv1alpha1.EnvironmentPhaseCASetup
		if err := r.Status().Update(ctx, env); err != nil {
			return ctrl.Result{}, err
		}

		result, err := r.reconcileCASetupJob(ctx, env)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling CA setup job: %w", err)
		}
		if result.Requeue || result.RequeueAfter > 0 {
			return result, nil
		}

		// Job succeeded — create CA marker Secret
		logger.Info("CA setup complete, creating marker Secret")
		markerSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      caSecretName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
			Data: map[string][]byte{
				"initialized": []byte("true"),
			},
		}
		if err := controllerutil.SetControllerReference(env, markerSecret, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, markerSecret); err != nil {
			if !errors.IsAlreadyExists(err) {
				return ctrl.Result{}, err
			}
		}
	}

	// Step 2c: CA Service
	if err := r.reconcileCAService(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA Service: %w", err)
	}

	// Update status
	env.Status.CAReady = true
	env.Status.CASecretName = caSecretName
	env.Status.CAServiceName = fmt.Sprintf("%s-ca", env.Name)
	env.Status.Phase = openvoxv1alpha1.EnvironmentPhaseRunning
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionCAInitialized,
		Status:             metav1.ConditionTrue,
		Reason:             "CASecretExists",
		Message:            "CA certificates are initialized",
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Update(ctx, env); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Environment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// --- ConfigMap ---

func (r *EnvironmentReconciler) reconcileConfigMap(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	logger := log.FromContext(ctx)
	configMapName := fmt.Sprintf("%s-config", env.Name)

	data := map[string]string{
		"puppet.conf":       r.renderPuppetConf(env),
		"puppetdb.conf":     r.renderPuppetDBConf(env),
		"webserver.conf":    r.renderWebserverConf(env),
		"puppetserver.conf": r.renderPuppetserverConf(env),
		"product.conf":      "product: {\n    check-for-updates: false\n}\n",
		"ca-enabled.cfg":    "puppetlabs.services.ca.certificate-authority-service/certificate-authority-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
		"ca-disabled.cfg":   "puppetlabs.services.ca.certificate-authority-disabled-service/certificate-authority-disabled-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
	}

	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: env.Namespace}, cm)
	if errors.IsNotFound(err) {
		logger.Info("creating ConfigMap", "name", configMapName)
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(env, cm, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, cm)
	} else if err != nil {
		return err
	}

	cm.Data = data
	return r.Update(ctx, cm)
}

func (r *EnvironmentReconciler) renderPuppetConf(env *openvoxv1alpha1.Environment) string {
	var sb strings.Builder
	sb.WriteString("[main]\n")
	sb.WriteString("confdir = /etc/puppetlabs/puppet\n")
	sb.WriteString("vardir = /opt/puppetlabs/puppet/cache\n")
	sb.WriteString("logdir = /var/log/puppetlabs/puppet\n")
	sb.WriteString("codedir = /etc/puppetlabs/code\n")
	sb.WriteString("rundir = /var/run/puppetlabs\n")
	sb.WriteString("manage_internal_file_permissions = false\n")

	if env.Spec.Puppet.EnvironmentPath != "" {
		sb.WriteString(fmt.Sprintf("environmentpath = %s\n", env.Spec.Puppet.EnvironmentPath))
	}

	if env.Spec.Puppet.HieraConfig != "" {
		sb.WriteString(fmt.Sprintf("hiera_config = %s\n", env.Spec.Puppet.HieraConfig))
	}

	sb.WriteString("\n[server]\n")

	if env.Spec.Puppet.EnvironmentTimeout != "" {
		sb.WriteString(fmt.Sprintf("environment_timeout = %s\n", env.Spec.Puppet.EnvironmentTimeout))
	}

	if env.Spec.Puppet.Storeconfigs {
		sb.WriteString("storeconfigs = true\n")
		if env.Spec.Puppet.StoreBackend != "" {
			sb.WriteString(fmt.Sprintf("storeconfigs_backend = %s\n", env.Spec.Puppet.StoreBackend))
		}
	}

	if env.Spec.Puppet.Reports != "" {
		sb.WriteString(fmt.Sprintf("reports = %s\n", env.Spec.Puppet.Reports))
	}

	if env.Spec.CA.TTL > 0 {
		sb.WriteString(fmt.Sprintf("ca_ttl = %d\n", env.Spec.CA.TTL))
	}
	if env.Spec.CA.Autosign != "" {
		sb.WriteString(fmt.Sprintf("autosign = %s\n", env.Spec.CA.Autosign))
	}

	if len(env.Spec.CA.DNSAltNames) > 0 {
		sb.WriteString(fmt.Sprintf("dns_alt_names = %s\n", strings.Join(env.Spec.CA.DNSAltNames, ",")))
	}

	if env.Spec.CA.Certname != "" {
		sb.WriteString(fmt.Sprintf("certname = %s\n", env.Spec.CA.Certname))
	}

	for k, v := range env.Spec.Puppet.ExtraConfig {
		sb.WriteString(fmt.Sprintf("%s = %s\n", k, v))
	}

	return sb.String()
}

func (r *EnvironmentReconciler) renderPuppetDBConf(env *openvoxv1alpha1.Environment) string {
	if len(env.Spec.PuppetDB.ServerURLs) == 0 {
		return "[main]\nserver_urls = https://openvoxdb:8081\nsoft_write_failure = true\n"
	}
	return fmt.Sprintf("[main]\nserver_urls = %s\nsoft_write_failure = true\n",
		strings.Join(env.Spec.PuppetDB.ServerURLs, ","))
}

func (r *EnvironmentReconciler) renderWebserverConf(env *openvoxv1alpha1.Environment) string {
	return `webserver: {
    ssl-host: 0.0.0.0
    ssl-port: 8140
    ssl-cert: /etc/puppetlabs/puppet/ssl/certs/puppet.pem
    ssl-key: /etc/puppetlabs/puppet/ssl/private_keys/puppet.pem
    ssl-ca-cert: /etc/puppetlabs/puppet/ssl/certs/ca.pem
    ssl-crl-path: /etc/puppetlabs/puppet/ssl/crl.pem
}
`
}

func (r *EnvironmentReconciler) renderPuppetserverConf(env *openvoxv1alpha1.Environment) string {
	return `jruby-puppet: {
    ruby-load-path: [/opt/puppetlabs/puppet/lib/ruby/vendor_ruby]
    gem-home: /opt/puppetlabs/server/data/puppetserver/jruby-gems
    gem-path: [${jruby-puppet.gem-home}, "/opt/puppetlabs/server/data/puppetserver/vendored-jruby-gems", "/opt/puppetlabs/puppet/lib/ruby/vendor_gems"]
    master-conf-dir: /etc/puppetlabs/puppet
    master-code-dir: /etc/puppetlabs/code
    master-var-dir: /opt/puppetlabs/server/data/puppetserver
    master-run-dir: /var/run/puppetlabs/puppetserver
    master-log-dir: /var/log/puppetlabs/puppetserver
    max-active-instances: 1
    max-requests-per-instance: 0
}

http-client: {
}

profiler: {
}

dropsonde: {
    enabled: false
}
`
}

// --- CA PVC ---

func (r *EnvironmentReconciler) reconcileCAPVC(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	pvcName := fmt.Sprintf("%s-ca-data", env.Name)

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: env.Namespace}, pvc)
	if errors.IsNotFound(err) {
		storageSize := "1Gi"
		if env.Spec.CA.Storage.Size != "" {
			storageSize = env.Spec.CA.Storage.Size
		}

		pvc = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(storageSize),
					},
				},
			},
		}

		if env.Spec.CA.Storage.StorageClass != "" {
			pvc.Spec.StorageClassName = &env.Spec.CA.Storage.StorageClass
		}

		if err := controllerutil.SetControllerReference(env, pvc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, pvc)
	}
	return err
}

// --- CA Setup Job ---

func (r *EnvironmentReconciler) reconcileCASetupJob(ctx context.Context, env *openvoxv1alpha1.Environment) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	jobName := fmt.Sprintf("%s-ca-setup", env.Name)

	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: env.Namespace}, job)
	if errors.IsNotFound(err) {
		logger.Info("creating CA setup job", "name", jobName)
		job = r.buildCASetupJob(env, jobName)
		if err := controllerutil.SetControllerReference(env, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, job); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	if job.Status.Succeeded > 0 {
		logger.Info("CA setup job completed successfully")
		return ctrl.Result{}, nil
	}

	if job.Status.Failed > 0 {
		return ctrl.Result{}, fmt.Errorf("CA setup job failed")
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *EnvironmentReconciler) buildCASetupJob(env *openvoxv1alpha1.Environment, name string) *batchv1.Job {
	image := fmt.Sprintf("%s:%s", env.Spec.Image.Repository, env.Spec.Image.Tag)
	backoffLimit := int32(3)

	setupScript := `#!/bin/bash
set -euo pipefail
echo "Starting CA setup..."
puppetserver ca setup \
    --config /etc/puppetlabs/puppetserver/conf.d
echo "CA setup complete."
`

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: env.Namespace,
			Labels:    environmentLabels(env.Name),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: environmentLabels(env.Name),
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    int64Ptr(1001),
						RunAsGroup:   int64Ptr(0),
						RunAsNonRoot: boolPtr(true),
					},
					Containers: []corev1.Container{
						{
							Name:    "ca-setup",
							Image:   image,
							Command: []string{"/bin/bash", "-c", setupScript},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "ca-data",
									MountPath: "/etc/puppetlabs/puppetserver/ca",
								},
								{
									Name:      "ssl",
									MountPath: "/etc/puppetlabs/puppet/ssl",
								},
								{
									Name:      "puppet-conf",
									MountPath: "/etc/puppetlabs/puppet/puppet.conf",
									SubPath:   "puppet.conf",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "ca-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: fmt.Sprintf("%s-ca-data", env.Name),
								},
							},
						},
						{
							Name: "ssl",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "puppet-conf",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-config", env.Name),
									},
									Items: []corev1.KeyToPath{
										{Key: "puppet.conf", Path: "puppet.conf"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// --- CA Service ---

func (r *EnvironmentReconciler) reconcileCAService(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	svcName := fmt.Sprintf("%s-ca", env.Name)

	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: env.Namespace}, svc)
	if errors.IsNotFound(err) {
		labels := environmentLabels(env.Name)
		// CA Service selects pods with role=ca in this environment
		selector := map[string]string{
			LabelEnvironment: env.Name,
			LabelRole:        RoleCA,
		}

		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: env.Namespace,
				Labels:    labels,
			},
			Spec: corev1.ServiceSpec{
				Selector: selector,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       8140,
						TargetPort: intstr.FromInt32(8140),
						Protocol:   corev1.ProtocolTCP,
					},
				},
			},
		}

		if err := controllerutil.SetControllerReference(env, svc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, svc)
	}
	return err
}
