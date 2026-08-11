using System.Text.Json.Serialization;

namespace NeProto.App;

public sealed record ProfileDto(
    [property: JsonPropertyName("id")] string Id,
    [property: JsonPropertyName("name")] string Name,
    [property: JsonPropertyName("server_identity")] string ServerIdentity,
    [property: JsonPropertyName("server_addresses")] string[] ServerAddresses,
    [property: JsonPropertyName("region")] string? Region,
    [property: JsonPropertyName("cluster_id")] string? ClusterId,
    [property: JsonPropertyName("cluster_available")] bool ClusterAvailable,
    [property: JsonPropertyName("cluster_node_id")] string? ClusterNodeId);

public sealed record ProfilesPayload(
    [property: JsonPropertyName("profiles")] ProfileDto[] Profiles,
    [property: JsonPropertyName("selected_profile_id")] string? SelectedProfileId);

public sealed record StatusDto(
    [property: JsonPropertyName("state")] string State,
    [property: JsonPropertyName("last_error")] string? LastError,
    [property: JsonPropertyName("selected_profile_id")] string? SelectedProfileId,
    [property: JsonPropertyName("profile_name")] string? ProfileName,
    [property: JsonPropertyName("server_identity")] string? ServerIdentity,
    [property: JsonPropertyName("connected_since")] DateTimeOffset? ConnectedSince,
    [property: JsonPropertyName("carrier")] string? Carrier,
    [property: JsonPropertyName("upload_bytes_per_second")] long UploadBytesPerSecond,
    [property: JsonPropertyName("download_bytes_per_second")] long DownloadBytesPerSecond,
    [property: JsonPropertyName("upload_total_bytes")] long UploadTotalBytes,
    [property: JsonPropertyName("download_total_bytes")] long DownloadTotalBytes,
    [property: JsonPropertyName("udp_mode")] string? UdpMode,
    [property: JsonPropertyName("carrier_pool_target")] long CarrierPoolTarget,
    [property: JsonPropertyName("carrier_pool_healthy")] long CarrierPoolHealthy);

public sealed record LogEntryDto(
    [property: JsonPropertyName("time")] DateTimeOffset Time,
    [property: JsonPropertyName("level")] string Level,
    [property: JsonPropertyName("message")] string Message);

public sealed record RouteDto(
    [property: JsonPropertyName("id")] string Id,
    [property: JsonPropertyName("name")] string Name,
    [property: JsonPropertyName("description")] string Description,
    [property: JsonPropertyName("mandatory")] bool Mandatory,
    [property: JsonPropertyName("enabled")] bool Enabled,
    [property: JsonPropertyName("source")] string Source)
{
    public bool CanDelete => Source == "client";
}
