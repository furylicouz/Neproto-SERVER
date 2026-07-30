import Foundation
import Security

public enum KeychainSecretStoreError: Error, Equatable, LocalizedError {
    case itemNotFound
    case corruptData
    case unexpectedStatus(OSStatus)

    public var errorDescription: String? {
        switch self {
        case .itemNotFound: "Ключ профиля не найден в Keychain."
        case .corruptData: "Keychain вернул повреждённый ключ профиля."
        case let .unexpectedStatus(status): "Ошибка Keychain (\(status))."
        }
    }
}

public final class KeychainSecretStore {
    public static let defaultService = "ru.neproto.ios.profile-secret"

    private let service: String
    private let accessGroup: String?
    private let keychain: any KeychainAccessing

    public init(service: String = defaultService, accessGroup: String?) {
        self.service = service
        self.accessGroup = accessGroup
        keychain = SystemKeychainAccess()
    }

    init(service: String, accessGroup: String?, keychain: any KeychainAccessing) {
        self.service = service
        self.accessGroup = accessGroup
        self.keychain = keychain
    }

    public convenience init(bundle: Bundle = .main) {
        let rawGroup = bundle.object(forInfoDictionaryKey: "NeProtoKeychainAccessGroup") as? String
        let resolvedGroup = rawGroup.flatMap { value in
            value.isEmpty || value.contains("$(") ? nil : value
        }
        self.init(accessGroup: resolvedGroup)
    }

    @discardableResult
    public func save(secret: String, profileID: UUID) throws -> Data {
        do {
            try delete(profileID: profileID)
        } catch KeychainSecretStoreError.itemNotFound {
            // Replacing a missing value is the normal first-save path.
        }

        var query = baseQuery(profileID: profileID)
        query[kSecValueData as String] = Data(secret.utf8)
        query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        query[kSecReturnPersistentRef as String] = true

        let (status, result) = keychain.add(query as CFDictionary)
        guard status == errSecSuccess else {
            throw Self.error(status)
        }
        guard let persistentReference = result as? Data, !persistentReference.isEmpty else {
            throw KeychainSecretStoreError.corruptData
        }
        return persistentReference
    }

    public func persistentReference(profileID: UUID) throws -> Data {
        var query = baseQuery(profileID: profileID)
        query[kSecReturnPersistentRef as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        let (status, result) = keychain.copyMatching(query as CFDictionary)
        guard status == errSecSuccess else {
            throw Self.error(status)
        }
        guard let persistentReference = result as? Data, !persistentReference.isEmpty else {
            throw KeychainSecretStoreError.corruptData
        }
        return persistentReference
    }

    public func read(persistentReference: Data) throws -> String {
        let query: [String: Any] = [
            kSecValuePersistentRef as String: persistentReference,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        let (status, result) = keychain.copyMatching(query as CFDictionary)
        guard status == errSecSuccess else {
            throw Self.error(status)
        }
        guard let data = result as? Data, let secret = String(data: data, encoding: .utf8), !secret.isEmpty else {
            throw KeychainSecretStoreError.corruptData
        }
        return secret
    }

    public func read(profileID: UUID) throws -> String {
        try read(persistentReference: persistentReference(profileID: profileID))
    }

    public func delete(profileID: UUID) throws {
        let status = keychain.delete(baseQuery(profileID: profileID) as CFDictionary)
        guard status == errSecSuccess else {
            throw Self.error(status)
        }
    }

    private func baseQuery(profileID: UUID) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: profileID.uuidString.lowercased(),
        ]
        if let accessGroup {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        return query
    }

    private static func error(_ status: OSStatus) -> KeychainSecretStoreError {
        status == errSecItemNotFound ? .itemNotFound : .unexpectedStatus(status)
    }
}

protocol KeychainAccessing {
    func add(_ query: CFDictionary) -> (OSStatus, CFTypeRef?)
    func copyMatching(_ query: CFDictionary) -> (OSStatus, CFTypeRef?)
    func delete(_ query: CFDictionary) -> OSStatus
}

private struct SystemKeychainAccess: KeychainAccessing {
    func add(_ query: CFDictionary) -> (OSStatus, CFTypeRef?) {
        var result: CFTypeRef?
        return (SecItemAdd(query, &result), result)
    }

    func copyMatching(_ query: CFDictionary) -> (OSStatus, CFTypeRef?) {
        var result: CFTypeRef?
        return (SecItemCopyMatching(query, &result), result)
    }

    func delete(_ query: CFDictionary) -> OSStatus {
        SecItemDelete(query)
    }
}
