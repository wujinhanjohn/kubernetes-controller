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

	kappsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1 "github.com/wujinhanjohn/webapp-operator/api/v1"
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the WebApp object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *WebAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var webapp appsv1.WebApp
	if err := r.Get(ctx, req.NamespacedName, &webapp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	desired := r.buildDeployment(&webapp)

	if err := ctrl.SetControllerReference(&webapp, desired, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	var existing kappsv1.Deployment
	err := r.Get(ctx, req.NamespacedName, &existing)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating new deployment", "name", req.Name)
		if err := r.Create(ctx, desired); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	needsUpdate := false
	if existing.Spec.Replicas == nil || *existing.Spec.Replicas != *desired.Spec.Replicas {
		needsUpdate = true
	}
	if len(existing.Spec.Template.Spec.Containers) > 0 && existing.Spec.Template.Spec.Containers[0].Image != desired.Spec.Template.Spec.Containers[0].Image {
		needsUpdate = true
	}
	if needsUpdate {
		logger.Info("Update Deployment", "name", desired.Name)
		existing.Spec = desired.Spec
		if err := r.Update(ctx, &existing); err != nil {
			return ctrl.Result{}, err
		}
	}

	webapp.Status.AvailableReplicas = existing.Status.AvailableReplicas
	if err := r.Status().Update(ctx, &webapp); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WebAppReconciler) buildDeployment(webapp *appsv1.WebApp) *kappsv1.Deployment {
	replicas := webapp.Spec.Replicas
	if replicas == 0 {
		replicas = 1
	}
	port := webapp.Spec.Port
	if port == 0 {
		port = 8080
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
						Name:  "app",
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
