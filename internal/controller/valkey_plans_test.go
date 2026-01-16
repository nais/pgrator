package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var _ = Describe("Valkey Plans", func() {
	Describe("machineTypeFromTierAndMemory", func() {
		Context("SingleNode tier", func() {
			It("should map 1GB to hobbyist plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierSingleNode, v1.ValkeyMemory1GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("hobbyist"))
			})

			It("should map 4GB to startup-4 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierSingleNode, v1.ValkeyMemory4GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("startup-4"))
			})

			It("should map 8GB to startup-8 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierSingleNode, v1.ValkeyMemory8GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("startup-8"))
			})

			It("should map 14GB to startup-14 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierSingleNode, v1.ValkeyMemory14GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("startup-14"))
			})

			It("should map 28GB to startup-28 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierSingleNode, v1.ValkeyMemory28GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("startup-28"))
			})

			It("should map 56GB to startup-56 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierSingleNode, v1.ValkeyMemory56GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("startup-56"))
			})

			It("should map 112GB to startup-112 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierSingleNode, v1.ValkeyMemory112GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("startup-112"))
			})

			It("should map 200GB to startup-200 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierSingleNode, v1.ValkeyMemory200GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("startup-200"))
			})
		})

		Context("HighAvailability tier", func() {
			It("should map 1GB to business-1 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierHighAvailability, v1.ValkeyMemory1GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("business-1"))
			})

			It("should map 4GB to business-4 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierHighAvailability, v1.ValkeyMemory4GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("business-4"))
			})

			It("should map 8GB to business-8 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierHighAvailability, v1.ValkeyMemory8GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("business-8"))
			})

			It("should map 14GB to business-14 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierHighAvailability, v1.ValkeyMemory14GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("business-14"))
			})

			It("should map 28GB to business-28 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierHighAvailability, v1.ValkeyMemory28GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("business-28"))
			})

			It("should map 56GB to business-56 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierHighAvailability, v1.ValkeyMemory56GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("business-56"))
			})

			It("should map 112GB to business-112 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierHighAvailability, v1.ValkeyMemory112GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("business-112"))
			})

			It("should map 200GB to business-200 plan", func() {
				machine, err := machineTypeFromTierAndMemory(v1.ValkeyTierHighAvailability, v1.ValkeyMemory200GB)
				Expect(err).NotTo(HaveOccurred())
				Expect(machine.AivenPlan).To(Equal("business-200"))
			})
		})

		Context("error cases", func() {
			It("should return error for invalid tier", func() {
				_, err := machineTypeFromTierAndMemory("InvalidTier", v1.ValkeyMemory4GB)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid Valkey tier"))
			})

			It("should return error for invalid memory", func() {
				_, err := machineTypeFromTierAndMemory(v1.ValkeyTierSingleNode, "InvalidMemory")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid Valkey memory"))
			})
		})
	})

	Describe("machineTypeFromPlan", func() {
		It("should return correct tier and memory for hobbyist plan", func() {
			machine, err := machineTypeFromPlan("hobbyist")
			Expect(err).NotTo(HaveOccurred())
			Expect(machine.Tier).To(Equal(v1.ValkeyTierSingleNode))
			Expect(machine.Memory).To(Equal(v1.ValkeyMemory1GB))
		})

		It("should return correct tier and memory for startup-4 plan", func() {
			machine, err := machineTypeFromPlan("startup-4")
			Expect(err).NotTo(HaveOccurred())
			Expect(machine.Tier).To(Equal(v1.ValkeyTierSingleNode))
			Expect(machine.Memory).To(Equal(v1.ValkeyMemory4GB))
		})

		It("should return correct tier and memory for business-8 plan", func() {
			machine, err := machineTypeFromPlan("business-8")
			Expect(err).NotTo(HaveOccurred())
			Expect(machine.Tier).To(Equal(v1.ValkeyTierHighAvailability))
			Expect(machine.Memory).To(Equal(v1.ValkeyMemory8GB))
		})

		It("should return correct tier and memory for business-200 plan", func() {
			machine, err := machineTypeFromPlan("business-200")
			Expect(err).NotTo(HaveOccurred())
			Expect(machine.Tier).To(Equal(v1.ValkeyTierHighAvailability))
			Expect(machine.Memory).To(Equal(v1.ValkeyMemory200GB))
		})

		It("should return error for invalid plan", func() {
			_, err := machineTypeFromPlan("invalid-plan")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid Valkey plan"))
		})
	})
})
