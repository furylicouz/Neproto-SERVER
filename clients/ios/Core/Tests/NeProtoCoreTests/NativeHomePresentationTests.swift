import Foundation
import Testing
@testable import NeProtoCore

@Suite("Native iOS home presentation")
struct NativeHomePresentationTests {
    @Test("profiles from one cluster form one subscription in source order")
    func groupsClusterProfiles() {
        let bootstrap = profile(
            name: "Основная подписка",
            identity: "primary.example.com",
            clusterID: "cluster-one",
            managedByCluster: false,
            region: "RU"
        )
        let amsterdam = profile(
            name: "Амстердам",
            identity: "nl.example.com",
            clusterID: "cluster-one",
            managedByCluster: true,
            region: "NL"
        )

        let sections = NativeHomePresentation.subscriptions(from: [bootstrap, amsterdam])

        #expect(sections.count == 1)
        #expect(sections[0].id == "cluster:cluster-one")
        #expect(sections[0].title == "Основная подписка")
        #expect(sections[0].profiles.map(\.id) == [bootstrap.id, amsterdam.id])
    }

    @Test("standalone profiles share a native servers section")
    func groupsStandaloneProfiles() {
        let first = profile(name: "Москва", identity: "one.example.com", region: "Москва")
        let second = profile(name: "Primary", identity: "two.example.com")

        let sections = NativeHomePresentation.subscriptions(from: [first, second])

        #expect(sections.count == 1)
        #expect(sections[0].id == "standalone")
        #expect(sections[0].title == "Серверы")
        #expect(sections[0].profiles.map(\.id) == [first.id, second.id])
    }

    @Test("location emoji uses an ISO flag with a safe globe fallback")
    func locationEmoji() {
        let netherlands = profile(name: "Amsterdam", identity: "nl.example.com", region: "NL")
        let unknown = profile(name: "Primary", identity: "primary.example.com", region: "Private")

        #expect(NativeHomePresentation.locationEmoji(for: netherlands) == "🇳🇱")
        #expect(NativeHomePresentation.locationEmoji(for: unknown) == "🌐")
    }

    @Test("bootstrap location emoji falls back to a location in the server name")
    func bootstrapLocationEmojiFromName() {
        let moscow = profile(name: "Moscow Edge", identity: "edge.example.org")

        #expect(NativeHomePresentation.locationEmoji(for: moscow) == "🇷🇺")
    }

    @Test("bootstrap location emoji falls back to an ISO country domain")
    func bootstrapLocationEmojiFromCountryDomain() {
        let netherlands = profile(name: "Primary", identity: "edge.example.nl")

        #expect(NativeHomePresentation.locationEmoji(for: netherlands) == "🇳🇱")
    }

    @Test("subscription disclosure is expanded by default and toggles deterministically")
    func subscriptionDisclosure() {
        var state = NativeSubscriptionDisclosureState()

        #expect(state.isExpanded(subscriptionID: "cluster:one"))
        state.toggle(subscriptionID: "cluster:one")
        #expect(!state.isExpanded(subscriptionID: "cluster:one"))
        state.toggle(subscriptionID: "cluster:one")
        #expect(state.isExpanded(subscriptionID: "cluster:one"))
    }

    @Test("automatic ping includes only available subscription servers")
    func automaticPingProfiles() {
        let available = profile(name: "Moscow", identity: "one.example.com", region: "RU")
        var unavailable = profile(name: "Amsterdam", identity: "two.example.com", region: "NL")
        unavailable.clusterAvailable = false
        let subscription = NativeSubscriptionSection(
            id: "cluster:test",
            title: "Test",
            profiles: [available, unavailable]
        )

        #expect(NativeHomePresentation.pingableProfiles(in: subscription).map(\.id) == [available.id])
    }

    private func profile(
        name: String,
        identity: String,
        clusterID: String? = nil,
        managedByCluster: Bool = false,
        region: String? = nil
    ) -> ServerProfile {
        ServerProfile(
            name: name,
            serverIdentity: identity,
            serverAddress: "203.0.113.10",
            httpsPath: "/1234567890abcdef",
            webRTCPath: "/abcdef1234567890",
            http3Path: "/fedcba0987654321",
            clusterID: clusterID,
            managedByCluster: managedByCluster,
            region: region,
            coverProfile: .web
        )
    }
}
