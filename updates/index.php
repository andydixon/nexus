<?php
$platform = isset($_GET['platform']) ? $_GET['platform'] : '';
$arch = isset($_GET['arch']) ? $_GET['arch'] : '';
$platform = preg_replace('/[^a-z0-9_-]/i', '', $platform);
$arch = preg_replace('/[^a-z0-9_-]/i', '', $arch);

$manifestPath = __DIR__ . '/manifest.json';
$versionPath = __DIR__ . '/version.txt';
$manifestFiles = glob(__DIR__ . '/manifest-*-*.json') ?: [];
if ($platform && $arch) {
    $manifestPath = __DIR__ . '/manifest-' . $platform . '-' . $arch . '.json';
    $versionPath = __DIR__ . '/version-' . $platform . '-' . $arch . '.txt';
}

$accept = isset($_SERVER['HTTP_ACCEPT']) ? $_SERVER['HTTP_ACCEPT'] : '';
$format = isset($_GET['format']) ? strtolower($_GET['format']) : '';

$wantsJson = $format === 'json' || strpos($accept, 'application/json') !== false;
$wantsVersion = $format === 'version';

$manifest = null;
if (is_readable($manifestPath)) {
    $raw = file_get_contents($manifestPath);
    $decoded = json_decode($raw, true);
    if (json_last_error() === JSON_ERROR_NONE && is_array($decoded)) {
        $manifest = $decoded;
    }
}

$allManifests = [];
foreach ($manifestFiles as $path) {
    $raw = file_get_contents($path);
    $decoded = json_decode($raw, true);
    if (json_last_error() === JSON_ERROR_NONE && is_array($decoded)) {
        $allManifests[] = $decoded;
    }
}

if ($wantsJson) {
    header('Content-Type: application/json; charset=utf-8');
    if ($manifest) {
        echo json_encode($manifest, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES);
    } else {
        http_response_code(503);
        echo json_encode(['error' => 'manifest unavailable'], JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES);
    }
    exit;
}

if ($wantsVersion) {
    header('Content-Type: text/plain; charset=utf-8');
    if ($manifest && !empty($manifest['latestVersion'])) {
        echo $manifest['latestVersion'];
    } elseif (is_readable($versionPath)) {
        echo trim(file_get_contents($versionPath));
    } else {
        http_response_code(503);
        echo 'unknown';
    }
    exit;
}

if ($platform && $arch && $format === 'download') {
    if ($manifest && !empty($manifest['files'][0]['url'])) {
        header('Location: ' . $manifest['files'][0]['url'], true, 302);
    } else {
        http_response_code(404);
        echo 'download not available';
    }
    exit;
}

function h($value)
{
    return htmlspecialchars((string)$value, ENT_QUOTES, 'UTF-8');
}

function formatSize($bytes)
{
    if (!is_numeric($bytes)) {
        return '';
    }
    $bytes = (float)$bytes;
    $units = ['B', 'KB', 'MB', 'GB'];
    $i = 0;
    while ($bytes >= 1024 && $i < count($units) - 1) {
        $bytes /= 1024;
        $i++;
    }
    return number_format($bytes, $i === 0 ? 0 : 2) . ' ' . $units[$i];
}

$latest = $manifest && isset($manifest['latestVersion']) ? $manifest['latestVersion'] : 'unknown';
$published = $manifest && isset($manifest['publishedAt']) ? $manifest['publishedAt'] : '';
$platformLabel = $platform && $arch ? strtoupper($platform) . ' / ' . $arch : '';
$files = $manifest && isset($manifest['files']) && is_array($manifest['files']) ? $manifest['files'] : [];

$rows = [];
if (!$platform && !$arch) {
    foreach ($allManifests as $entry) {
        $file = isset($entry['files'][0]) ? $entry['files'][0] : null;
        if (!$file) {
            continue;
        }
        $rows[] = [
            'platform' => isset($file['platform']) ? $file['platform'] : '',
            'arch' => isset($file['arch']) ? $file['arch'] : '',
            'version' => isset($entry['latestVersion']) ? $entry['latestVersion'] : 'unknown',
            'size' => isset($file['size']) ? $file['size'] : '',
            'url' => isset($file['url']) ? $file['url'] : '',
            'filename' => isset($file['filename']) ? $file['filename'] : '',
        ];
    }
    usort($rows, function ($a, $b) {
        $ka = $a['platform'] . '/' . $a['arch'] . '/' . $a['version'];
        $kb = $b['platform'] . '/' . $b['arch'] . '/' . $b['version'];
        return strcmp($ka, $kb);
    });
}
?>
<!doctype html>
<html lang="en">

<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Nexus Terminal Downloads</title>
    <style>
        :root {
            --bg: #111318;
            --panel: #1b1e24;
            --text: #e7e9ee;
            --muted: #9aa2b2;
            --accent: #4ecdc4;
        }

        body {
            margin: 0;
            font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif;
            background: var(--bg);
            color: var(--text);
        }

        .wrap {
            max-width: 960px;
            margin: 0 auto;
            padding: 32px 20px 60px;
        }

        h1 {
            margin: 0 0 6px;
            font-size: 28px;
        }

        .meta {
            color: var(--muted);
            font-size: 14px;
            margin-bottom: 24px;
        }

        .card {
            background: var(--panel);
            border: 1px solid #2a2f3a;
            border-radius: 12px;
            padding: 20px;
        }

        table {
            width: 100%;
            border-collapse: collapse;
            margin-top: 12px;
        }

        th,
        td {
            text-align: left;
            padding: 10px 8px;
            border-bottom: 1px solid #2a2f3a;
            font-size: 14px;
        }

        th {
            color: var(--muted);
            font-weight: 600;
        }

        a {
            color: var(--accent);
            text-decoration: none;
        }

        a:hover {
            text-decoration: underline;
        }

        .badge {
            display: inline-block;
            padding: 2px 8px;
            border-radius: 999px;
            background: rgba(78, 205, 196, 0.12);
            color: var(--accent);
            font-size: 12px;
        }

        .empty {
            color: var(--muted);
        }
    </style>
</head>

<body>
    <div class="wrap">
        <h1>Nexus Terminal Downloads</h1>
        <div class="meta">Latest version: <span class="badge"><?php echo h($latest); ?></span><?php if ($platformLabel) {
                                                                                                    echo ' • ' . h($platformLabel);
                                                                                                } ?><?php if ($published) {
                                                                                                        echo ' • Published ' . h($published);
                                                                                                    } ?></div>

        <div class="card">
            <h2 style="margin:0 0 10px;font-size:18px;">Release files</h2>
            <?php if (!$platform && !$arch) { ?>
                <?php if (empty($rows)) { ?>
                    <div class="empty">No release files available.</div>
                <?php } else { ?>
                    <table>
                        <thead>
                            <tr>
                                <th>Platform</th>
                                <th>CPU</th>
                                <th>Version</th>
                                <th>Size</th>
                                <th>Download</th>
                            </tr>
                        </thead>
                        <tbody>
                            <?php foreach ($rows as $row) { ?>
                                <?php
                                $downloadLink = '';
                                if ($row['platform'] && $row['arch']) {
                                    $downloadLink = '?platform=' . urlencode($row['platform']) . '&arch=' . urlencode($row['arch']) . '&format=download';
                                }
                                ?>
                                <tr>
                                    <td><?php echo h($row['platform']); ?></td>
                                    <td><?php echo h($row['arch']); ?></td>
                                    <td><?php echo h($row['version']); ?></td>
                                    <td><?php echo h(formatSize($row['size'])); ?></td>
                                    <td>
                                        <?php if ($downloadLink) { ?>
                                            <a href="<?php echo h($downloadLink); ?>">Download</a>
                                        <?php } else { ?>
                                            <?php echo h($row['filename']); ?>
                                        <?php } ?>
                                    </td>
                                </tr>
                            <?php } ?>
                        </tbody>
                    </table>
                <?php } ?>
            <?php } else { ?>
                <?php if (empty($files)) { ?>
                    <div class="empty">No release files available.</div>
                <?php } else { ?>
                    <table>
                        <thead>
                            <tr>
                                <th>Platform</th>
                                <th>Arch</th>
                                <th>File</th>
                                <th>Size</th>
                                <th>SHA-256</th>
                            </tr>
                        </thead>
                        <tbody>
                            <?php foreach ($files as $file) {
                                $platform = isset($file['platform']) ? $file['platform'] : '';
                                $arch = isset($file['arch']) ? $file['arch'] : '';
                                $filename = isset($file['filename']) ? $file['filename'] : '';
                                $url = isset($file['url']) ? $file['url'] : '';
                                $size = isset($file['size']) ? $file['size'] : '';
                                $sha = isset($file['sha256']) ? $file['sha256'] : '';
                            ?>
                                <tr>
                                    <td><?php echo h($platform); ?></td>
                                    <td><?php echo h($arch); ?></td>
                                    <td>
                                        <?php if ($url) { ?>
                                            <a href="<?php echo h($url); ?>"><?php echo h($filename); ?></a>
                                        <?php } else { ?>
                                            <?php echo h($filename); ?>
                                        <?php } ?>
                                    </td>
                                    <td><?php echo h(formatSize($size)); ?></td>
                                    <td style="font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; color: var(--muted);">
                                        <?php echo h($sha); ?>
                                    </td>
                                </tr>
                            <?php } ?>
                        </tbody>
                    </table>
                <?php } ?>
            <?php } ?>
        </div>
    </div>
</body>

</html>