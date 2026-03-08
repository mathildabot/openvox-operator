package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// CertificateReconciler reconciles a Certificate object.
type CertificateReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *CertificateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cert := &openvoxv1alpha1.Certificate{}
	if err := r.Get(ctx, req.NamespacedName, cert); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if cert.Status.Phase == "" {
		cert.Status.Phase = openvoxv1alpha1.CertificatePhasePending
		if err := r.Status().Update(ctx, cert); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolve CertificateAuthority
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cert.Spec.AuthorityRef, Namespace: cert.Namespace}, ca); err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "referenced CertificateAuthority not found", "authorityRef", cert.Spec.AuthorityRef)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Wait for CA to be ready
	if ca.Status.Phase != openvoxv1alpha1.CertificateAuthorityPhaseReady {
		logger.Info("waiting for CertificateAuthority to be ready", "ca", ca.Name, "phase", ca.Status.Phase)
		cert.Status.Phase = openvoxv1alpha1.CertificatePhasePending
		_ = r.Status().Update(ctx, cert)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Resolve Environment (needed for image)
	env := &openvoxv1alpha1.Environment{}
	if err := r.Get(ctx, types.NamespacedName{Name: ca.Spec.EnvironmentRef, Namespace: cert.Namespace}, env); err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "referenced Environment not found", "environmentRef", ca.Spec.EnvironmentRef)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	tlsSecretName := fmt.Sprintf("%s-tls", cert.Name)

	// Check if TLS Secret already exists (may have been created by CA setup job)
	if r.isSecretReady(ctx, tlsSecretName, cert.Namespace, "cert.pem") {
		// Adopt the Secret by setting ownerReference to this Certificate
		if err := r.adoptTLSSecret(ctx, cert, tlsSecretName); err != nil {
			return ctrl.Result{}, fmt.Errorf("adopting TLS Secret: %w", err)
		}

		cert.Status.Phase = openvoxv1alpha1.CertificatePhaseSigned
		cert.Status.SecretName = tlsSecretName
		meta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionCertSigned,
			Status:             metav1.ConditionTrue,
			Reason:             "CertificateSigned",
			Message:            "Certificate is signed and available",
			LastTransitionTime: metav1.Now(),
		})
		if err := r.Status().Update(ctx, cert); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Ensure RBAC for cert job
	if err := r.reconcileCertRBAC(ctx, cert, ca); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling cert RBAC: %w", err)
	}

	// Sign via HTTP bootstrap against running CA server
	result, err := r.reconcileCertJob(ctx, cert, ca, env)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling cert job: %w", err)
	}
	if result.Requeue || result.RequeueAfter > 0 {
		return result, nil
	}

	// Certificate is signed
	cert.Status.Phase = openvoxv1alpha1.CertificatePhaseSigned
	cert.Status.SecretName = tlsSecretName
	meta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionCertSigned,
		Status:             metav1.ConditionTrue,
		Reason:             "CertificateSigned",
		Message:            "Certificate is signed and available",
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Update(ctx, cert); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *CertificateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Certificate{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ServiceAccount{}).
		Watches(&corev1.Secret{}, enqueueCertificatesForSecret(mgr.GetClient())).
		Complete(r)
}

// --- RBAC ---

func (r *CertificateReconciler) reconcileCertRBAC(ctx context.Context, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority) error {
	baseName := fmt.Sprintf("%s-cert-setup", cert.Name)
	tlsSecretName := fmt.Sprintf("%s-tls", cert.Name)
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)
	labels := environmentLabels(ca.Spec.EnvironmentRef)
	labels["openvox.voxpupuli.org/certificate"] = cert.Name

	resourceNames := []string{tlsSecretName, caSecretName}

	if err := r.ensureCertServiceAccount(ctx, baseName, cert.Namespace, labels, cert); err != nil {
		return fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	if err := r.ensureCertRole(ctx, baseName, cert.Namespace, labels, resourceNames, cert); err != nil {
		return fmt.Errorf("ensuring Role: %w", err)
	}

	if err := r.ensureCertRoleBinding(ctx, baseName, cert.Namespace, labels, cert); err != nil {
		return fmt.Errorf("ensuring RoleBinding: %w", err)
	}

	return nil
}

func (r *CertificateReconciler) ensureCertServiceAccount(ctx context.Context, name, namespace string, labels map[string]string, owner *openvoxv1alpha1.Certificate) error {
	sa := &corev1.ServiceAccount{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sa); errors.IsNotFound(err) {
		sa = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
		}
		if err := controllerutil.SetControllerReference(owner, sa, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, sa)
	} else {
		return err
	}
}

func (r *CertificateReconciler) ensureCertRole(ctx context.Context, name, namespace string, labels map[string]string, resourceNames []string, owner *openvoxv1alpha1.Certificate) error {
	role := &rbacv1.Role{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, role)
	if errors.IsNotFound(err) {
		role = &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					ResourceNames: resourceNames,
					Verbs:         []string{"get", "update", "patch"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"secrets"},
					Verbs:     []string{"create"},
				},
			},
		}
		if err := controllerutil.SetControllerReference(owner, role, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, role)
	} else if err != nil {
		return err
	}

	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups:     []string{""},
			Resources:     []string{"secrets"},
			ResourceNames: resourceNames,
			Verbs:         []string{"get", "update", "patch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"create"},
		},
	}
	return r.Update(ctx, role)
}

func (r *CertificateReconciler) ensureCertRoleBinding(ctx context.Context, name, namespace string, labels map[string]string, owner *openvoxv1alpha1.Certificate) error {
	rb := &rbacv1.RoleBinding{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, rb); errors.IsNotFound(err) {
		rb = &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      name,
					Namespace: namespace,
				},
			},
		}
		if err := controllerutil.SetControllerReference(owner, rb, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, rb)
	} else {
		return err
	}
}

// --- Cert Job ---

func (r *CertificateReconciler) reconcileCertJob(ctx context.Context, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, env *openvoxv1alpha1.Environment) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	tlsSecretName := fmt.Sprintf("%s-tls", cert.Name)

	// Wait for a running CA server to bootstrap against
	caServiceName := r.findCAServiceName(ctx, ca, cert.Namespace)
	if caServiceName == "" {
		logger.Info("waiting for CA server to become available")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	cert.Status.Phase = openvoxv1alpha1.CertificatePhaseRequesting
	_ = r.Status().Update(ctx, cert)

	jobName := fmt.Sprintf("%s-cert-setup", cert.Name)
	logger.Info("using HTTP cert bootstrap", "caService", caServiceName)
	job := r.buildHTTPCertJob(cert, ca, env, jobName, caServiceName)

	return r.reconcileJob(ctx, cert, jobName, job, tlsSecretName)
}

// findCAServiceName discovers the CA service endpoint by:
// 1. Finding Servers with ca:true in the same environment
// 2. Finding Pools whose selector matches the CA server pods
// 3. Returning the first matching Pool name as service name
func (r *CertificateReconciler) findCAServiceName(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, namespace string) string {
	// Find servers with CA role in this environment
	serverList := &openvoxv1alpha1.ServerList{}
	if err := r.List(ctx, serverList, client.InNamespace(namespace)); err != nil {
		return ""
	}

	hasRunningCAServer := false
	for _, server := range serverList.Items {
		if server.Spec.EnvironmentRef == ca.Spec.EnvironmentRef && server.Spec.CA {
			if server.Status.Phase == openvoxv1alpha1.ServerPhaseRunning {
				hasRunningCAServer = true
				break
			}
		}
	}

	if !hasRunningCAServer {
		return ""
	}

	// Find pools with CA selector in this environment
	poolList := &openvoxv1alpha1.PoolList{}
	if err := r.List(ctx, poolList, client.InNamespace(namespace)); err != nil {
		return ""
	}

	for _, pool := range poolList.Items {
		if pool.Spec.EnvironmentRef != ca.Spec.EnvironmentRef {
			continue
		}
		if pool.Spec.Selector[LabelCA] == "true" {
			return pool.Name
		}
	}

	return ""
}

func (r *CertificateReconciler) buildHTTPCertJob(cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, env *openvoxv1alpha1.Environment, jobName, caServiceName string) *batchv1.Job {
	image := fmt.Sprintf("%s:%s", env.Spec.Image.Repository, env.Spec.Image.Tag)
	backoffLimit := int32(3)
	saName := fmt.Sprintf("%s-cert-setup", cert.Name)
	tlsSecretName := fmt.Sprintf("%s-tls", cert.Name)
	labels := environmentLabels(ca.Spec.EnvironmentRef)
	labels["openvox.voxpupuli.org/certificate"] = cert.Name

	certname := cert.Spec.Certname
	if certname == "" {
		certname = "puppet"
	}

	script := buildServerCertScript()

	envVars := []corev1.EnvVar{
		{Name: "CERTNAME", Value: certname},
		{Name: "DNS_ALT_NAMES", Value: strings.Join(cert.Spec.DNSAltNames, ",")},
		{Name: "SSL_SECRET_NAME", Value: tlsSecretName},
		{Name: "CA_SERVICE", Value: caServiceName},
		{Name: "ENV_NAME", Value: ca.Spec.EnvironmentRef},
		{Name: "SERVER_NAME", Value: cert.Name},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: cert.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: saName,
					RestartPolicy:      corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    int64Ptr(1001),
						RunAsGroup:   int64Ptr(0),
						RunAsNonRoot: boolPtr(true),
					},
					Containers: []corev1.Container{
						{
							Name:            "cert-setup",
							Image:           image,
							ImagePullPolicy: env.Spec.Image.PullPolicy,
							Command:         []string{"/bin/bash", "-c", script},
							Env:             envVars,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "ssl", MountPath: "/etc/puppetlabs/puppet/ssl"},
								{Name: "puppet-conf", MountPath: "/etc/puppetlabs/puppet/puppet.conf", SubPath: "puppet.conf", ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "ssl", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{
							Name: "puppet-conf",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-config", ca.Spec.EnvironmentRef),
									},
									Items: []corev1.KeyToPath{{Key: "puppet.conf", Path: "puppet.conf"}},
								},
							},
						},
					},
				},
			},
		},
	}
}

// --- Secret adoption ---

// adoptTLSSecret sets the ownerReference on the TLS Secret to this Certificate,
// so that deleting the Certificate garbage-collects the Secret.
func (r *CertificateReconciler) adoptTLSSecret(ctx context.Context, cert *openvoxv1alpha1.Certificate, secretName string) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: cert.Namespace}, secret); err != nil {
		return err
	}

	// Check if already owned by this Certificate
	for _, ref := range secret.OwnerReferences {
		if ref.UID == cert.UID {
			return nil
		}
	}

	if err := controllerutil.SetControllerReference(cert, secret, r.Scheme); err != nil {
		return err
	}
	return r.Update(ctx, secret)
}

// --- Job lifecycle management ---

func (r *CertificateReconciler) reconcileJob(ctx context.Context, cert *openvoxv1alpha1.Certificate, jobName string, desiredJob *batchv1.Job, expectedSecretName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	existingJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: cert.Namespace}, existingJob)
	if errors.IsNotFound(err) {
		logger.Info("creating cert setup job", "name", jobName)
		if err := controllerutil.SetControllerReference(cert, desiredJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, desiredJob); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check if image changed
	desiredImage := desiredJob.Spec.Template.Spec.Containers[0].Image
	currentImage := ""
	if len(existingJob.Spec.Template.Spec.Containers) > 0 {
		currentImage = existingJob.Spec.Template.Spec.Containers[0].Image
	}
	if currentImage != desiredImage {
		return r.deleteAndRequeueJob(ctx, existingJob, "image changed")
	}

	if existingJob.Status.Succeeded > 0 {
		if !r.isSecretReady(ctx, expectedSecretName, cert.Namespace, "cert.pem") {
			logger.Info("job succeeded but secret missing, recreating", "name", jobName)
			return r.deleteAndRequeueJob(ctx, existingJob, "secret missing after success")
		}
		logger.Info("cert setup job completed successfully", "name", jobName)
		return ctrl.Result{}, nil
	}

	// Check permanent failure
	for _, c := range existingJob.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return r.deleteAndRequeueJob(ctx, existingJob, "permanently failed")
		}
	}

	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

func (r *CertificateReconciler) deleteAndRequeueJob(ctx context.Context, job *batchv1.Job, reason string) (ctrl.Result, error) {
	log.FromContext(ctx).Info("deleting cert setup job", "name", job.Name, "reason", reason)
	propagation := metav1.DeletePropagationForeground
	if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *CertificateReconciler) isSecretReady(ctx context.Context, name, namespace, requiredKey string) bool {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		return false
	}
	if requiredKey != "" {
		_, ok := secret.Data[requiredKey]
		return ok
	}
	return true
}
