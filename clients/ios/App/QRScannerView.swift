@preconcurrency import AVFoundation
import SwiftUI
import UIKit

enum QRScannerError: Error, LocalizedError {
    case cameraDenied
    case cameraUnavailable

    var errorDescription: String? {
        switch self {
        case .cameraDenied: "Разрешите NeProto доступ к камере в Настройках iOS."
        case .cameraUnavailable: "Не удалось запустить камеру для сканирования QR-кода."
        }
    }
}

struct QRScannerView: View {
    @Environment(\.dismiss) private var dismiss
    let completion: (Result<String, Error>) -> Void

    var body: some View {
        NavigationStack {
            ZStack {
                QRScannerCamera(completion: completion)
                    .ignoresSafeArea()
                RoundedRectangle(cornerRadius: 24)
                    .stroke(.white, lineWidth: 3)
                    .frame(width: 260, height: 260)
                    .shadow(radius: 8)
                VStack {
                    Spacer()
                    Text("Наведите камеру на QR-код NP/2")
                        .font(.headline)
                        .padding(.horizontal, 20)
                        .padding(.vertical, 12)
                        .background(.ultraThinMaterial, in: Capsule())
                        .padding(.bottom, 36)
                }
            }
            .navigationTitle("Добавить NP/2")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Отмена") { dismiss() }
                }
            }
        }
    }
}

private struct QRScannerCamera: UIViewControllerRepresentable {
    let completion: (Result<String, Error>) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(completion: completion) }

    func makeUIViewController(context: Context) -> CameraViewController {
        let controller = CameraViewController()
        controller.start(coordinator: context.coordinator)
        return controller
    }

    func updateUIViewController(_ uiViewController: CameraViewController, context: Context) {}

    static func dismantleUIViewController(_ uiViewController: CameraViewController, coordinator: Coordinator) {
        uiViewController.stop()
    }

    final class Coordinator: NSObject, AVCaptureMetadataOutputObjectsDelegate, @unchecked Sendable {
        private let completion: (Result<String, Error>) -> Void
        private var completed = false

        init(completion: @escaping (Result<String, Error>) -> Void) {
            self.completion = completion
        }

        func fail(_ error: Error) {
            guard !completed else { return }
            completed = true
            completion(.failure(error))
        }

        func metadataOutput(
            _ output: AVCaptureMetadataOutput,
            didOutput metadataObjects: [AVMetadataObject],
            from connection: AVCaptureConnection
        ) {
            guard !completed,
                  let object = metadataObjects.first as? AVMetadataMachineReadableCodeObject,
                  object.type == .qr,
                  let value = object.stringValue else {
                return
            }
            completed = true
            completion(.success(value))
        }
    }
}

private final class CameraViewController: UIViewController {
    private let session = AVCaptureSession()
    private var preview: AVCaptureVideoPreviewLayer?

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        preview?.frame = view.bounds
    }

    func start(coordinator: QRScannerCamera.Coordinator) {
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized:
            configure(coordinator: coordinator)
        case .notDetermined:
            AVCaptureDevice.requestAccess(for: .video) { [weak self] allowed in
                DispatchQueue.main.async {
                    if allowed {
                        self?.configure(coordinator: coordinator)
                    } else {
                        coordinator.fail(QRScannerError.cameraDenied)
                    }
                }
            }
        default:
            coordinator.fail(QRScannerError.cameraDenied)
        }
    }

    func stop() {
        if session.isRunning { session.stopRunning() }
    }

    private func configure(coordinator: QRScannerCamera.Coordinator) {
        guard let camera = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: camera),
              session.canAddInput(input) else {
            coordinator.fail(QRScannerError.cameraUnavailable)
            return
        }
        session.addInput(input)

        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else {
            coordinator.fail(QRScannerError.cameraUnavailable)
            return
        }
        session.addOutput(output)
        output.setMetadataObjectsDelegate(coordinator, queue: .main)
        output.metadataObjectTypes = [.qr]

        let layer = AVCaptureVideoPreviewLayer(session: session)
        layer.videoGravity = .resizeAspectFill
        view.layer.insertSublayer(layer, at: 0)
        preview = layer
        DispatchQueue.global(qos: .userInitiated).async { [session] in
            session.startRunning()
        }
    }
}
