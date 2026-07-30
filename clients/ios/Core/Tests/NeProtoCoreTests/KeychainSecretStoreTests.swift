import Foundation
import Security
import Testing
@testable import NeProtoCore

@Suite("NP/2 Keychain secret store", .serialized)
struct KeychainSecretStoreTests {
    @Test("secret round-trips only through a persistent reference and can be deleted")
    func persistentReferenceLifecycle() throws {
        let service = "ru.neproto.tests.\(UUID().uuidString)"
        let profileID = UUID()
        let store = KeychainSecretStore(service: service, accessGroup: nil, keychain: InMemoryKeychain())
        let secret = Data(repeating: 0x6B, count: 32)
            .base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        defer { try? store.delete(profileID: profileID) }

        let persistentReference = try store.save(secret: secret, profileID: profileID)

        #expect(!persistentReference.isEmpty)
        #expect(try store.read(persistentReference: persistentReference) == secret)
        #expect(try store.read(profileID: profileID) == secret)

        try store.delete(profileID: profileID)
        #expect(throws: KeychainSecretStoreError.itemNotFound) {
            try store.persistentReference(profileID: profileID)
        }
    }
}

private final class InMemoryKeychain: KeychainAccessing {
    private var values: [String: Data] = [:]
    private var references: [Data: String] = [:]

    func add(_ query: CFDictionary) -> (OSStatus, CFTypeRef?) {
        let dictionary = query as NSDictionary
        guard let account = dictionary[kSecAttrAccount] as? String,
              let value = dictionary[kSecValueData] as? Data,
              values[account] == nil else {
            return (errSecDuplicateItem, nil)
        }
        let reference = Data(("persistent:" + account).utf8)
        values[account] = value
        references[reference] = account
        return (errSecSuccess, reference as CFData)
    }

    func copyMatching(_ query: CFDictionary) -> (OSStatus, CFTypeRef?) {
        let dictionary = query as NSDictionary
        let account: String?
        if let reference = dictionary[kSecValuePersistentRef] as? Data {
            account = references[reference]
        } else {
            account = dictionary[kSecAttrAccount] as? String
        }
        guard let account, let value = values[account] else {
            return (errSecItemNotFound, nil)
        }
        if dictionary[kSecReturnData] as? Bool == true {
            return (errSecSuccess, value as CFData)
        }
        let reference = Data(("persistent:" + account).utf8)
        return (errSecSuccess, reference as CFData)
    }

    func delete(_ query: CFDictionary) -> OSStatus {
        let dictionary = query as NSDictionary
        guard let account = dictionary[kSecAttrAccount] as? String, values.removeValue(forKey: account) != nil else {
            return errSecItemNotFound
        }
        references = references.filter { $0.value != account }
        return errSecSuccess
    }
}
