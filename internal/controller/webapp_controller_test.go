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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	kappsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "github.com/wujinhanjohn/webapp-operator/api/v1"
)

var _ = Describe("WebApp Controller", func() {
	const (
		resourceName      = "test-resource"
		resourceNamespace = "default"
		resourceImage     = "nginx:1.25"
		resourcePort      = int32(8080)
		resourceReplicas  = int32(2)
	)

	ctx := context.Background()

	typeNamespacedName := types.NamespacedName{
		Name:      resourceName,
		Namespace: resourceNamespace,
	}

	// reconciler returns a reconciler wired to the envtest client.
	reconciler := func() *WebAppReconciler {
		return &WebAppReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	}

	// doReconcile runs a single reconcile pass for the shared resource name.
	doReconcile := func() error {
		_, err := reconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		return err
	}

	// createWebApp persists a WebApp with the given spec.
	createWebApp := func(spec appsv1.WebAppSpec) {
		Expect(k8sClient.Create(ctx, &appsv1.WebApp{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: resourceNamespace},
			Spec:       spec,
		})).To(Succeed())
	}

	AfterEach(func() {
		// Delete the WebApp (and its owned Deployment) if the test created one.
		webapp := &appsv1.WebApp{}
		if err := k8sClient.Get(ctx, typeNamespacedName, webapp); err == nil {
			Expect(k8sClient.Delete(ctx, webapp)).To(Succeed())
		}
		deployment := &kappsv1.Deployment{}
		if err := k8sClient.Get(ctx, typeNamespacedName, deployment); err == nil {
			Expect(k8sClient.Delete(ctx, deployment)).To(Succeed())
		}
	})

	Context("when reconciling a newly created resource", func() {
		It("creates a Deployment owned by the WebApp with the desired spec", func() {
			createWebApp(appsv1.WebAppSpec{
				Image:    resourceImage,
				Replicas: resourceReplicas,
				Port:     resourcePort,
			})

			Expect(doReconcile()).To(Succeed())

			deployment := &kappsv1.Deployment{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, deployment)).To(Succeed())

			Expect(deployment.Spec.Replicas).NotTo(BeNil())
			Expect(*deployment.Spec.Replicas).To(Equal(resourceReplicas))
			Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))

			container := deployment.Spec.Template.Spec.Containers[0]
			Expect(container.Image).To(Equal(resourceImage))
			Expect(container.Ports).To(HaveLen(1))
			Expect(container.Ports[0].ContainerPort).To(Equal(resourcePort))

			By("owning the Deployment via a controller reference")
			Expect(deployment.OwnerReferences).To(HaveLen(1))
			owner := deployment.OwnerReferences[0]
			Expect(owner.Kind).To(Equal("WebApp"))
			Expect(owner.Name).To(Equal(resourceName))
			Expect(owner.Controller).NotTo(BeNil())
			Expect(*owner.Controller).To(BeTrue())
		})
	})

	Context("when the spec omits optional fields", func() {
		It("applies the default replica count and port", func() {
			// Only image is set; the API server applies CRD defaults for the rest.
			createWebApp(appsv1.WebAppSpec{Image: resourceImage})

			Expect(doReconcile()).To(Succeed())

			deployment := &kappsv1.Deployment{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, deployment)).To(Succeed())

			Expect(deployment.Spec.Replicas).NotTo(BeNil())
			Expect(*deployment.Spec.Replicas).To(Equal(defaultReplicas))
			Expect(deployment.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort).To(Equal(defaultPort))
		})
	})

	Context("when the live Deployment has drifted from the spec", func() {
		It("reconciles the replica count and image back to the desired state", func() {
			createWebApp(appsv1.WebAppSpec{
				Image:    resourceImage,
				Replicas: resourceReplicas,
				Port:     resourcePort,
			})
			Expect(doReconcile()).To(Succeed())

			By("mutating the WebApp spec")
			webapp := &appsv1.WebApp{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, webapp)).To(Succeed())
			webapp.Spec.Replicas = 5
			webapp.Spec.Image = "nginx:1.27"
			Expect(k8sClient.Update(ctx, webapp)).To(Succeed())

			Expect(doReconcile()).To(Succeed())

			By("observing the Deployment converge to the new spec")
			deployment := &kappsv1.Deployment{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, deployment)).To(Succeed())
			Expect(*deployment.Spec.Replicas).To(Equal(int32(5)))
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal("nginx:1.27"))
		})
	})

	Context("when reporting status", func() {
		It("mirrors available replicas and sets the Available condition", func() {
			createWebApp(appsv1.WebAppSpec{
				Image:    resourceImage,
				Replicas: resourceReplicas,
				Port:     resourcePort,
			})
			Expect(doReconcile()).To(Succeed())

			By("simulating the Deployment reporting available replicas")
			deployment := &kappsv1.Deployment{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, deployment)).To(Succeed())
			// The apiserver enforces availableReplicas <= readyReplicas <= replicas.
			deployment.Status.Replicas = resourceReplicas
			deployment.Status.ReadyReplicas = resourceReplicas
			deployment.Status.AvailableReplicas = resourceReplicas
			Expect(k8sClient.Status().Update(ctx, deployment)).To(Succeed())

			Expect(doReconcile()).To(Succeed())

			webapp := &appsv1.WebApp{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, webapp)).To(Succeed())
			Expect(webapp.Status.AvailableReplicas).To(Equal(resourceReplicas))

			condition := findCondition(webapp.Status.Conditions, conditionAvailable)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		})

		It("marks Available=False while replicas are still unavailable", func() {
			createWebApp(appsv1.WebAppSpec{
				Image:    resourceImage,
				Replicas: resourceReplicas,
				Port:     resourcePort,
			})
			Expect(doReconcile()).To(Succeed())

			webapp := &appsv1.WebApp{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, webapp)).To(Succeed())
			Expect(webapp.Status.AvailableReplicas).To(Equal(int32(0)))

			condition := findCondition(webapp.Status.Conditions, conditionAvailable)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Context("when the WebApp no longer exists", func() {
		It("reconciles cleanly without error", func() {
			// No resource created; reconcile should ignore the not-found lookup.
			Expect(doReconcile()).To(Succeed())

			deployment := &kappsv1.Deployment{}
			err := k8sClient.Get(ctx, typeNamespacedName, deployment)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})
})

// findCondition returns a pointer to the condition of the given type, or nil.
func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
