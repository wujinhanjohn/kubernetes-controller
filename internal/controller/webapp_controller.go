/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	kappsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1 "github.com/wujinhanjohn/webapp-operator/api/v1"
)

const (
	// defaultReplicas is used when a WebApp does not specify a replica count.
	defaultReplicas int32 = 1
	// defaultPort is used when a WebApp does not specify a container port.
	defaultPort int32 = 8080
	// containerName is the name of the single container in the managed Deployment.
	containerName = "app"
	// conditionAvailable reports whether the managed Deployment has reached its
	// desired replica count.
	conditionAvailable = "Available"
)

// WebAppReconciler reconciles a WebApp object
type WebAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=apps.example.com,resources=webapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.example.com,resources=webapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.example.com,resources=webapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives the cluster toward the state declared by a WebApp. It
// ensures a Deployment of the same name exists and owned by the WebApp,
// reconciles the Deployment's replica count and container image when they
// drift from the spec, and mirrors the Deployment's availability back into the
// WebApp's status (available replica count and the "Available" condition).
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *WebAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var webapp appsv1.WebApp
	if err := r.Get(ctx, req.NamespacedName, &webapp); err != nil {
		// The WebApp was deleted; owned Deployments are garbage-collected.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	desired := r.buildDeployment(&webapp)
	if err := ctrl.SetControllerReference(&webapp, desired, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	var existing kappsv1.Deployment
	err := r.Get(ctx, req.NamespacedName, &existing)
	switch {
	case apierrors.IsNotFound(err):
		logger.Info("Creating Deployment", "name", desired.Name)
		if err := r.Create(ctx, desired); err != nil {
			return ctrl.Result{}, err
		}
		// A freshly created Deployment reports no available replicas yet.
		return ctrl.Result{}, r.updateStatus(ctx, &webapp, 0, *desired.Spec.Replicas)
	case err != nil:
		return ctrl.Result{}, err
	}

	if deploymentNeedsUpdate(&existing, desired) {
		logger.Info("Updating Deployment", "name", desired.Name)
		existing.Spec = desired.Spec
		if err := r.Update(ctx, &existing); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, r.updateStatus(ctx, &webapp, existing.Status.AvailableReplicas, *desired.Spec.Replicas)
}

// updateStatus writes the observed availability back to the WebApp, but only
// issues an API call when the status actually changed, avoiding needless writes
// and status-update conflict churn on otherwise no-op reconciles.
func (r *WebAppReconciler) updateStatus(ctx context.Context, webapp *appsv1.WebApp, available, desired int32) error {
	changed := webapp.Status.AvailableReplicas != available
	webapp.Status.AvailableReplicas = available

	condition := metav1.Condition{
		Type:    conditionAvailable,
		Status:  metav1.ConditionTrue,
		Reason:  "MinimumReplicasAvailable",
		Message: fmt.Sprintf("%d/%d replicas are available", available, desired),
	}
	if available < desired {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "MinimumReplicasUnavailable"
	}
	if meta.SetStatusCondition(&webapp.Status.Conditions, condition) {
		changed = true
	}

	if !changed {
		return nil
	}
	return r.Status().Update(ctx, webapp)
}

// deploymentNeedsUpdate reports whether the live Deployment has drifted from
// the desired spec in the fields this controller manages: replica count and
// the application container image.
func deploymentNeedsUpdate(existing, desired *kappsv1.Deployment) bool {
	if existing.Spec.Replicas == nil || *existing.Spec.Replicas != *desired.Spec.Replicas {
		return true
	}
	if len(existing.Spec.Template.Spec.Containers) == 0 {
		return true
	}
	return existing.Spec.Template.Spec.Containers[0].Image != desired.Spec.Template.Spec.Containers[0].Image
}

func (r *WebAppReconciler) buildDeployment(webapp *appsv1.WebApp) *kappsv1.Deployment {
	// The CRD sets these defaults for objects created through the API server;
	// re-applying them here keeps buildDeployment correct for direct callers
	// (e.g. unit tests) that construct a WebApp struct without the API defaults.
	replicas := webapp.Spec.Replicas
	if replicas == 0 {
		replicas = defaultReplicas
	}
	port := webapp.Spec.Port
	if port == 0 {
		port = defaultPort
	}
	labels := map[string]string{"app": webapp.Name}

	return &kappsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webapp.Name,
			Namespace: webapp.Namespace,
		},
		Spec: kappsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  containerName,
						Image: webapp.Spec.Image,
						Ports: []corev1.ContainerPort{{ContainerPort: port}},
					}},
				},
			},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.WebApp{}).
		Owns(&kappsv1.Deployment{}).
		Complete(r)
}
